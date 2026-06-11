package appconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

const opTimeout = 30 * time.Second

type settingsMsg struct {
	settings []azappconfig.Setting
	err      error
}

type opDoneMsg struct {
	action string
	err    error
}

// refreshMsg is handed back by the detail view (via ui.PopWith) after it
// modifies a setting, so the list reloads.
type refreshMsg struct{}

type newSpec struct {
	Key         string `yaml:"key"`
	Label       string `yaml:"label"`
	ContentType string `yaml:"content_type"`
	Value       string `yaml:"value"`
}

const newTemplate = `# aztui — new App Configuration setting.
# Save and quit to create; quit without saving changes to cancel.
key: ""
label: ""
content_type: ""
value: ""
`

// bulkEntry is one element of the JSON array used for bulk editing, in the
// spirit of the Azure portal's "advanced edit": change values, add entries
// to create settings, remove entries to delete them.
type bulkEntry struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Value       string `json:"value"`
	ContentType string `json:"content_type,omitempty"`
}

type bulkUpdate struct {
	orig  azappconfig.Setting
	entry bulkEntry
}

type bulkPlan struct {
	updates []bulkUpdate
	creates []bulkEntry
	deletes []azappconfig.Setting
}

func (p bulkPlan) empty() bool {
	return len(p.updates) == 0 && len(p.creates) == 0 && len(p.deletes) == 0
}

func (p bulkPlan) summary() string {
	return fmt.Sprintf("%d update(s), %d create(s), %d delete(s)",
		len(p.updates), len(p.creates), len(p.deletes))
}

type bulkDoneMsg struct {
	summary string
	failed  []string
}

func bulkKey(key, label string) string { return key + "\x00" + label }

// buildPlan diffs the edited entries against the originally selected
// settings: changed entries update, new entries create, missing entries
// delete — exactly how the portal's bulk edit behaves.
func buildPlan(orig map[string]azappconfig.Setting, entries []bulkEntry) (bulkPlan, error) {
	var plan bulkPlan
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Key == "" {
			return plan, fmt.Errorf("every entry needs a non-empty key")
		}
		k := bulkKey(e.Key, e.Label)
		if seen[k] {
			return plan, fmt.Errorf("duplicate entry for key %q label %q", e.Key, e.Label)
		}
		seen[k] = true
		if o, ok := orig[k]; ok {
			if deref(o.Value) != e.Value || deref(o.ContentType) != e.ContentType {
				plan.updates = append(plan.updates, bulkUpdate{orig: o, entry: e})
			}
		} else {
			plan.creates = append(plan.creates, e)
		}
	}
	for k, o := range orig {
		if !seen[k] {
			plan.deletes = append(plan.deletes, o)
		}
	}
	sort.Slice(plan.deletes, func(i, j int) bool {
		return deref(plan.deletes[i].Key) < deref(plan.deletes[j].Key)
	})
	return plan, nil
}

type listView struct {
	mctx   modules.Context
	res    azure.Resource
	client *azappconfig.Client

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	settings    []azappconfig.Setting
	pendingEdit int                            // settings index for an in-flight $EDITOR session
	bulkOrig    map[string]azappconfig.Setting // selection snapshot for an in-flight bulk edit

	width, height int
}

func newListView(mctx modules.Context, res azure.Resource, client *azappconfig.Client) *listView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "KEY", Weight: 5},
		ui.Column{Title: "LABEL", Weight: 2},
		ui.Column{Title: "VALUE", Weight: 6},
		ui.Column{Title: "UPDATED", Width: 8},
		ui.Column{Title: "RO", Width: 2},
	)
	t.Empty = "no settings in this store"
	t.Selectable = true
	return &listView{mctx: mctx, res: res, client: client, table: t, spin: sp, loading: true, pendingEdit: -1}
}

func (v *listView) Init() tea.Cmd {
	return tea.Batch(v.spin.Tick, v.load())
}

// fetchAllSettings drains the settings pager, sorted by key then label.
func fetchAllSettings(ctx context.Context, client *azappconfig.Client) ([]azappconfig.Setting, error) {
	pager := client.NewListSettingsPager(azappconfig.SettingSelector{}, nil)
	var all []azappconfig.Setting
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Settings...)
	}
	sort.Slice(all, func(i, j int) bool {
		ki, kj := deref(all[i].Key), deref(all[j].Key)
		if ki != kj {
			return ki < kj
		}
		return deref(all[i].Label) < deref(all[j].Label)
	})
	return all, nil
}

func (v *listView) load() tea.Cmd {
	client := v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		all, err := fetchAllSettings(ctx, client)
		return settingsMsg{settings: all, err: err}
	}
}

func (v *listView) setSettings(settings []azappconfig.Setting) {
	v.settings = settings
	rows := make([][]string, len(settings))
	for i, s := range settings {
		updated := "-"
		if s.LastModified != nil {
			updated = ui.Ago(*s.LastModified)
		}
		ro := ""
		if s.IsReadOnly != nil && *s.IsReadOnly {
			ro = "✓"
		}
		rows[i] = []string{deref(s.Key), deref(s.Label), deref(s.Value), updated, ro}
	}
	v.table.SetRows(rows)
}

func (v *listView) selected() (azappconfig.Setting, int, bool) {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.settings) {
		return azappconfig.Setting{}, -1, false
	}
	return v.settings[idx], idx, true
}

func (v *listView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case settingsMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setSettings(msg.settings)
		return v, nil

	case refreshMsg:
		v.loading = true
		return v, tea.Batch(v.spin.Tick, v.load())

	case opDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange(v.res.Name, msg.action)
		v.loading = true
		return v, tea.Batch(ui.Status("%s", msg.action), v.spin.Tick, v.load())

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case ui.EditorResult:
		return v, v.handleEditor(msg)

	case bulkDoneMsg:
		v.loading = true
		cmds := []tea.Cmd{v.spin.Tick, v.load()}
		if len(msg.failed) > 0 {
			cmds = append(cmds, ui.Errorf("%s — %d failed (first: %s)", msg.summary, len(msg.failed), msg.failed[0]))
		} else {
			ui.RecordChange(v.res.Name, msg.summary)
			cmds = append(cmds, ui.Status("%s", msg.summary))
		}
		return v, tea.Batch(cmds...)

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK {
				switch result.Tag {
				case "delete":
					return v, v.deleteCmd(result.Payload.(azappconfig.Setting))
				case "bulk":
					return v, bulkApplyCmd(v.client, result.Payload.(bulkPlan))
				}
			}
			return v, nil
		}
		if !v.table.InputActive() {
			if cmd, handled := v.handleAction(msg.String()); handled {
				return v, cmd
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *listView) handleAction(key string) (tea.Cmd, bool) {
	switch key {
	case "e", "n", "d", "L", "E":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
	}
	switch key {
	case "enter":
		if s, _, ok := v.selected(); ok {
			return ui.Push(newDetailView(v.res, v.client, s)), true
		}
	case "e":
		if s, idx, ok := v.selected(); ok {
			if s.IsReadOnly != nil && *s.IsReadOnly {
				return ui.Warnf("%s is read-only — press L to unlock first", deref(s.Key)), true
			}
			v.pendingEdit = idx
			return ui.OpenEditor("edit", []byte(deref(s.Value)), extFor(s)), true
		}
	case "n":
		return ui.OpenEditor("new", []byte(newTemplate), "yaml"), true
	case "E":
		sel := v.table.SelectedRows()
		if len(sel) == 0 {
			return ui.Warnf("select settings with space first (ctrl+a: all visible)"), true
		}
		v.bulkOrig = make(map[string]azappconfig.Setting, len(sel))
		entries := make([]bulkEntry, 0, len(sel))
		for _, idx := range sel {
			s := v.settings[idx]
			v.bulkOrig[bulkKey(deref(s.Key), deref(s.Label))] = s
			entries = append(entries, bulkEntry{
				Key:         deref(s.Key),
				Label:       deref(s.Label),
				Value:       deref(s.Value),
				ContentType: deref(s.ContentType),
			})
		}
		raw, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return ui.Err(err), true
		}
		return ui.OpenEditor("bulk", append(raw, '\n'), "json"), true
	case "d":
		if s, _, ok := v.selected(); ok {
			v.confirm.Ask("delete", fmt.Sprintf("Delete setting %q (label %q)?", deref(s.Key), deref(s.Label)), s)
			return nil, true
		}
	case "L":
		if s, _, ok := v.selected(); ok {
			return v.toggleLockCmd(s), true
		}
	case "D":
		return ui.Push(newPickerView(v.mctx, v.res, v.client)), true
	case "x":
		if s, _, ok := v.selected(); ok {
			return ui.Push(newCrossView(v.mctx, deref(s.Key), deref(s.Label))), true
		}
	case "y":
		if s, _, ok := v.selected(); ok {
			return ui.Yank(deref(s.Key), deref(s.Value)), true
		}
	case "R":
		v.loading = true
		return tea.Batch(v.spin.Tick, v.load()), true
	}
	return nil, false
}

func (v *listView) handleEditor(msg ui.EditorResult) tea.Cmd {
	if msg.Err != nil {
		return ui.Errorf("editor: %v", msg.Err)
	}
	if msg.Canceled {
		return ui.Status("no changes")
	}
	switch msg.Tag {
	case "edit":
		if v.pendingEdit < 0 || v.pendingEdit >= len(v.settings) {
			return nil
		}
		s := v.settings[v.pendingEdit]
		v.pendingEdit = -1
		return saveCmd(v.client, s, strings.TrimSuffix(string(msg.Content), "\n"))
	case "new":
		var spec newSpec
		if err := yaml.Unmarshal(msg.Content, &spec); err != nil {
			return ui.Errorf("invalid yaml: %v", err)
		}
		if spec.Key == "" {
			return ui.Errorf("key is required")
		}
		return createCmd(v.client, spec)
	case "bulk":
		var entries []bulkEntry
		if err := json.Unmarshal(msg.Content, &entries); err != nil {
			return ui.Errorf("invalid json: %v", err)
		}
		plan, err := buildPlan(v.bulkOrig, entries)
		if err != nil {
			return ui.Err(err)
		}
		if plan.empty() {
			return ui.Status("no changes")
		}
		v.confirm.Ask("bulk", fmt.Sprintf("Apply bulk edit: %s?", plan.summary()), plan)
		return nil
	}
	return nil
}

func (v *listView) deleteCmd(s azappconfig.Setting) tea.Cmd {
	client := v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, err := client.DeleteSetting(ctx, deref(s.Key), &azappconfig.DeleteSettingOptions{
			Label:           s.Label,
			OnlyIfUnchanged: s.ETag,
		})
		return opDoneMsg{action: fmt.Sprintf("deleted %s", deref(s.Key)), err: err}
	}
}

func (v *listView) toggleLockCmd(s azappconfig.Setting) tea.Cmd {
	client := v.client
	lock := s.IsReadOnly == nil || !*s.IsReadOnly
	verb := "locked"
	if !lock {
		verb = "unlocked"
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, err := client.SetReadOnly(ctx, deref(s.Key), lock, &azappconfig.SetReadOnlyOptions{Label: s.Label})
		return opDoneMsg{action: fmt.Sprintf("%s %s", verb, deref(s.Key)), err: err}
	}
}

// saveCmd writes a new value for an existing setting, preserving its label
// and content type, guarded by the setting's ETag.
func saveCmd(client *azappconfig.Client, s azappconfig.Setting, value string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, err := client.SetSetting(ctx, deref(s.Key), &value, &azappconfig.SetSettingOptions{
			Label:           s.Label,
			ContentType:     s.ContentType,
			OnlyIfUnchanged: s.ETag,
		})
		return opDoneMsg{action: fmt.Sprintf("updated %s", deref(s.Key)), err: err}
	}
}

// bulkApplyCmd executes a confirmed bulk plan sequentially. Updates and
// deletes are ETag-guarded, so settings changed by someone else mid-edit
// fail individually instead of being clobbered.
func bulkApplyCmd(client *azappconfig.Client, plan bulkPlan) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("bulk edit: %s", plan.summary())
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var failed []string
		fail := func(key string, err error) { failed = append(failed, key+": "+err.Error()) }

		for _, u := range plan.updates {
			value := u.entry.Value
			opts := &azappconfig.SetSettingOptions{
				Label:           u.orig.Label,
				OnlyIfUnchanged: u.orig.ETag,
			}
			if u.entry.ContentType != "" {
				opts.ContentType = to.Ptr(u.entry.ContentType)
			}
			if _, err := client.SetSetting(ctx, u.entry.Key, &value, opts); err != nil {
				fail(u.entry.Key, err)
			}
		}
		for _, e := range plan.creates {
			value := e.Value
			opts := &azappconfig.SetSettingOptions{}
			if e.Label != "" {
				opts.Label = to.Ptr(e.Label)
			}
			if e.ContentType != "" {
				opts.ContentType = to.Ptr(e.ContentType)
			}
			if _, err := client.SetSetting(ctx, e.Key, &value, opts); err != nil {
				fail(e.Key, err)
			}
		}
		for _, o := range plan.deletes {
			opts := &azappconfig.DeleteSettingOptions{Label: o.Label, OnlyIfUnchanged: o.ETag}
			if _, err := client.DeleteSetting(ctx, deref(o.Key), opts); err != nil {
				fail(deref(o.Key), err)
			}
		}
		return bulkDoneMsg{summary: "bulk edit: " + plan.summary(), failed: failed}
	}
}

func createCmd(client *azappconfig.Client, spec newSpec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		opts := &azappconfig.AddSettingOptions{}
		if spec.Label != "" {
			opts.Label = to.Ptr(spec.Label)
		}
		if spec.ContentType != "" {
			opts.ContentType = to.Ptr(spec.ContentType)
		}
		_, err := client.AddSetting(ctx, spec.Key, to.Ptr(spec.Value), opts)
		return opDoneMsg{action: fmt.Sprintf("created %s", spec.Key), err: err}
	}
}

func (v *listView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d settings", v.table.Count()))
	if n := v.table.SelectionCount(); n > 0 {
		title += ui.WarnStyle.Render(fmt.Sprintf("  · %d selected — E to bulk edit", n))
	}
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" loading settings...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *listView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *listView) Breadcrumb() string { return v.res.Name }

func (v *listView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view setting"},
		{Keys: "e", Desc: "edit value in $EDITOR"},
		{Keys: "space", Desc: "select / deselect"},
		{Keys: "ctrl+a", Desc: "select all visible"},
		{Keys: "E", Desc: "bulk edit selection as JSON"},
		{Keys: "D", Desc: "diff & sync with another store"},
		{Keys: "x", Desc: "this key across all stores"},
		{Keys: "y", Desc: "yank value"},
		{Keys: "n", Desc: "new setting"},
		{Keys: "d", Desc: "delete setting"},
		{Keys: "L", Desc: "lock/unlock (read-only)"},
		{Keys: "R", Desc: "refresh"},
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// extFor picks a temp-file extension so $EDITOR highlights sensibly.
func extFor(s azappconfig.Setting) string {
	ct := strings.ToLower(deref(s.ContentType))
	switch {
	case strings.Contains(ct, "json"):
		return "json"
	case strings.Contains(ct, "yaml"), strings.Contains(ct, "yml"):
		return "yaml"
	case strings.Contains(ct, "xml"):
		return "xml"
	default:
		return "txt"
	}
}
