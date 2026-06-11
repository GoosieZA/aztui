package appconfig

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

// --- target picker -----------------------------------------------------------

type pickerLoadedMsg struct {
	stores []azure.Resource
	err    error
}

// pickerView selects the store to diff the current one against.
type pickerView struct {
	mctx      modules.Context
	source    azure.Resource
	srcClient *azappconfig.Client

	table   ui.Table
	spin    spinner.Model
	loading bool

	stores []azure.Resource

	width, height int
}

func newPickerView(mctx modules.Context, source azure.Resource, srcClient *azappconfig.Client) *pickerView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "NAME", Weight: 4},
		ui.Column{Title: "RESOURCE GROUP", Weight: 3},
		ui.Column{Title: "SUBSCRIPTION", Weight: 3},
	)
	t.Empty = "no other App Configuration stores found"
	return &pickerView{mctx: mctx, source: source, srcClient: srcClient, table: t, spin: sp, loading: true}
}

func (v *pickerView) Init() tea.Cmd {
	cred, sourceID := v.mctx.Cred, v.source.ID
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		all, err := azure.DiscoverResources(ctx, cred, module{}.ResourceTypes())
		if err != nil {
			return pickerLoadedMsg{err: err}
		}
		stores := make([]azure.Resource, 0, len(all))
		for _, r := range all {
			if r.ID != sourceID {
				stores = append(stores, r)
			}
		}
		return pickerLoadedMsg{stores: stores}
	})
}

func (v *pickerView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case pickerLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.stores = msg.stores
		rows := make([][]string, len(msg.stores))
		for i, r := range msg.stores {
			sub := r.SubscriptionName
			if sub == "" {
				sub = r.SubscriptionID
			}
			rows[i] = []string{r.Name, r.ResourceGroup, sub}
		}
		v.table.SetRows(rows)
		return v, nil

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		if !v.table.InputActive() && msg.String() == "enter" {
			idx := v.table.CursorRow()
			if idx < 0 || idx >= len(v.stores) {
				return v, nil
			}
			target := v.stores[idx]
			endpoint, _, err := target.Endpoint()
			if err != nil {
				return v, ui.Err(err)
			}
			dstClient, err := azappconfig.NewClient(endpoint, v.mctx.Cred, nil)
			if err != nil {
				return v, ui.Err(err)
			}
			return v, ui.Push(newDiffView(v.source, v.srcClient, target, dstClient))
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *pickerView) View() string {
	title := ui.TitleStyle.Render(" diff "+v.source.Name+" against...") +
		ui.DimStyle.Render(fmt.Sprintf("  %d stores", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" finding stores...")
	}
	return title + "\n" + v.table.View()
}

func (v *pickerView) InputActive() bool { return v.table.InputActive() }

func (v *pickerView) Breadcrumb() string { return "diff" }

func (v *pickerView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{{Keys: "enter", Desc: "diff against this store"}}
}

// --- diff view ---------------------------------------------------------------

type diffRow struct {
	key, label string
	src, dst   *azappconfig.Setting
}

func (r diffRow) same() bool {
	return r.src != nil && r.dst != nil &&
		deref(r.src.Value) == deref(r.dst.Value) &&
		deref(r.src.ContentType) == deref(r.dst.ContentType)
}

func (r diffRow) status() string {
	switch {
	case r.src == nil:
		return "← target only"
	case r.dst == nil:
		return "→ source only"
	case r.same():
		return "= same"
	default:
		return "≠ differs"
	}
}

type diffLoadedMsg struct {
	src, dst []azappconfig.Setting
	err      error
}

type syncDoneMsg struct {
	summary string
	failed  []string
}

// syncPlan makes one store's selected entries match the other's.
type syncPlan struct {
	targetName string
	client     *azappconfig.Client
	sets       []syncSet
	deletes    []azappconfig.Setting
}

type syncSet struct {
	from azure.Resource       // unused placeholder to keep struct extensible
	src  azappconfig.Setting  // the desired state
	dst  *azappconfig.Setting // existing target setting, nil when creating
}

func (p syncPlan) summary() string {
	return fmt.Sprintf("%d set, %d delete → %s", len(p.sets), len(p.deletes), p.targetName)
}

// diffView is a toggleable side-by-side comparison of two stores: open it
// with D from a store, leave it with esc. Sync selected rows in either
// direction with > and <.
type diffView struct {
	src, dst             azure.Resource
	srcClient, dstClient *azappconfig.Client

	rows     []diffRow // full union
	display  []diffRow // what the table shows (diffs only unless showSame)
	showSame bool

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	width, height int
}

func newDiffView(src azure.Resource, srcClient *azappconfig.Client, dst azure.Resource, dstClient *azappconfig.Client) *diffView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "KEY", Weight: 4},
		ui.Column{Title: "LABEL", Width: 8},
		ui.Column{Title: "Δ", Width: 14},
		ui.Column{Title: src.Name, Weight: 3},
		ui.Column{Title: dst.Name, Weight: 3},
	)
	t.Empty = "stores are identical"
	t.Selectable = true
	return &diffView{src: src, dst: dst, srcClient: srcClient, dstClient: dstClient, table: t, spin: sp, loading: true}
}

func (v *diffView) Init() tea.Cmd {
	srcClient, dstClient := v.srcClient, v.dstClient
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		src, err := fetchAllSettings(ctx, srcClient)
		if err != nil {
			return diffLoadedMsg{err: fmt.Errorf("loading source: %w", err)}
		}
		dst, err := fetchAllSettings(ctx, dstClient)
		if err != nil {
			return diffLoadedMsg{err: fmt.Errorf("loading target: %w", err)}
		}
		return diffLoadedMsg{src: src, dst: dst}
	})
}

func (v *diffView) buildRows(src, dst []azappconfig.Setting) {
	byKey := map[string]*diffRow{}
	order := []string{}
	for i := range src {
		s := &src[i]
		k := bulkKey(deref(s.Key), deref(s.Label))
		byKey[k] = &diffRow{key: deref(s.Key), label: deref(s.Label), src: s}
		order = append(order, k)
	}
	for i := range dst {
		s := &dst[i]
		k := bulkKey(deref(s.Key), deref(s.Label))
		if row, ok := byKey[k]; ok {
			row.dst = s
		} else {
			byKey[k] = &diffRow{key: deref(s.Key), label: deref(s.Label), dst: s}
			order = append(order, k)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return ui.NaturalLess(order[i], order[j]) })
	v.rows = v.rows[:0]
	seen := map[string]bool{}
	for _, k := range order {
		if !seen[k] {
			seen[k] = true
			v.rows = append(v.rows, *byKey[k])
		}
	}
	v.refreshTable()
}

func (v *diffView) refreshTable() {
	v.display = v.display[:0]
	for _, r := range v.rows {
		if !v.showSame && r.same() {
			continue
		}
		v.display = append(v.display, r)
	}
	rows := make([][]string, len(v.display))
	for i, r := range v.display {
		srcVal, dstVal := "—", "—"
		if r.src != nil {
			srcVal = deref(r.src.Value)
		}
		if r.dst != nil {
			dstVal = deref(r.dst.Value)
		}
		rows[i] = []string{r.key, r.label, r.status(), srcVal, dstVal}
	}
	v.table.SetRows(rows)
}

func (v *diffView) counts() (differ, srcOnly, dstOnly, same int) {
	for _, r := range v.rows {
		switch {
		case r.src == nil:
			dstOnly++
		case r.dst == nil:
			srcOnly++
		case r.same():
			same++
		default:
			differ++
		}
	}
	return
}

// buildSyncPlan makes the receiving store match the giving store for the
// selected rows: differing rows are overwritten, missing rows created, and
// rows that exist only on the receiving side are deleted.
func (v *diffView) buildSyncPlan(toTarget bool) syncPlan {
	plan := syncPlan{targetName: v.dst.Name, client: v.dstClient}
	if !toTarget {
		plan.targetName = v.src.Name
		plan.client = v.srcClient
	}
	for _, idx := range v.table.SelectedRows() {
		if idx >= len(v.display) {
			continue
		}
		r := v.display[idx]
		from, to := r.src, r.dst
		if !toTarget {
			from, to = r.dst, r.src
		}
		switch {
		case from == nil && to != nil:
			plan.deletes = append(plan.deletes, *to)
		case from != nil && (to == nil || !r.same()):
			plan.sets = append(plan.sets, syncSet{src: *from, dst: to})
		}
	}
	return plan
}

func syncApplyCmd(plan syncPlan) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("syncing → %s", plan.targetName)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 2*opTimeout)
		defer cancel()
		var failed []string
		for _, s := range plan.sets {
			value := deref(s.src.Value)
			opts := &azappconfig.SetSettingOptions{
				Label:       s.src.Label,
				ContentType: s.src.ContentType,
			}
			if s.dst != nil {
				opts.OnlyIfUnchanged = s.dst.ETag
			}
			if _, err := plan.client.SetSetting(ctx, deref(s.src.Key), &value, opts); err != nil {
				failed = append(failed, deref(s.src.Key)+": "+err.Error())
			}
		}
		for _, d := range plan.deletes {
			opts := &azappconfig.DeleteSettingOptions{Label: d.Label, OnlyIfUnchanged: d.ETag}
			if _, err := plan.client.DeleteSetting(ctx, deref(d.Key), opts); err != nil {
				failed = append(failed, deref(d.Key)+": "+err.Error())
			}
		}
		return syncDoneMsg{summary: "synced: " + plan.summary(), failed: failed}
	}
}

func (v *diffView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case diffLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.buildRows(msg.src, msg.dst)
		return v, nil

	case syncDoneMsg:
		v.loading = true
		cmds := []tea.Cmd{v.spin.Tick, v.Init()}
		if len(msg.failed) > 0 {
			cmds = append(cmds, ui.Errorf("%s — %d failed (first: %s)", msg.summary, len(msg.failed), msg.failed[0]))
		} else {
			ui.RecordChange(v.src.Name+" → "+v.dst.Name, msg.summary)
			cmds = append(cmds, ui.Status("%s", msg.summary))
		}
		return v, tea.Batch(cmds...)

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "sync" {
				return v, syncApplyCmd(result.Payload.(syncPlan))
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

func (v *diffView) handleAction(key string) (tea.Cmd, bool) {
	switch key {
	case ">", "<":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
		if v.table.SelectionCount() == 0 {
			return ui.Warnf("select rows with space first (ctrl+a: all visible)"), true
		}
		plan := v.buildSyncPlan(key == ">")
		if len(plan.sets) == 0 && len(plan.deletes) == 0 {
			return ui.Status("selection already in sync"), true
		}
		v.confirm.Ask("sync", fmt.Sprintf("Sync %s? Deletes remove entries that only exist in the target.", plan.summary()), plan)
		return nil, true
	case "i":
		v.showSame = !v.showSame
		v.refreshTable()
		return nil, true
	case "enter":
		idx := v.table.CursorRow()
		if idx >= 0 && idx < len(v.display) {
			return ui.Push(newCompareView(v.src.Name, v.dst.Name, v.display[idx])), true
		}
		return nil, true
	case "R":
		v.loading = true
		return tea.Batch(v.spin.Tick, v.Init()), true
	}
	return nil, false
}

func (v *diffView) View() string {
	differ, srcOnly, dstOnly, same := v.counts()
	title := ui.TitleStyle.Render(" "+v.src.Name) + ui.DimStyle.Render(" vs ") + ui.TitleStyle.Render(v.dst.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  ≠%d →%d ←%d =%d", differ, srcOnly, dstOnly, same))
	if n := v.table.SelectionCount(); n > 0 {
		title += ui.WarnStyle.Render(fmt.Sprintf("  · %d selected", n))
	}
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" comparing stores...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *diffView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *diffView) Breadcrumb() string { return "diff " + v.dst.Name }

func (v *diffView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "space", Desc: "select / deselect"},
		{Keys: "ctrl+a", Desc: "select all visible"},
		{Keys: ">", Desc: "sync selection source → target"},
		{Keys: "<", Desc: "sync selection target → source"},
		{Keys: "i", Desc: "show/hide identical entries"},
		{Keys: "enter", Desc: "compare values"},
		{Keys: "R", Desc: "reload both stores"},
	}
}

// --- value compare detail ------------------------------------------------------

type compareView struct {
	srcName, dstName string
	row              diffRow

	vp            viewport.Model
	width, height int
}

func newCompareView(srcName, dstName string, row diffRow) *compareView {
	return &compareView{srcName: srcName, dstName: dstName, row: row}
}

func (v *compareView) Init() tea.Cmd { return nil }

func (v *compareView) content() string {
	var b strings.Builder
	section := func(name string, s *azappconfig.Setting) {
		b.WriteString(ui.TableHeaderStyle.Render(" "+name) + "\n")
		if s == nil {
			b.WriteString(ui.DimStyle.Render(" <not present>") + "\n\n")
			return
		}
		if ct := deref(s.ContentType); ct != "" {
			b.WriteString(ui.DimStyle.Render(" content type: "+ct) + "\n")
		}
		b.WriteString(prettify(deref(s.Value), deref(s.ContentType)) + "\n\n")
	}
	b.WriteString(ui.DimStyle.Render(" key   ") + ui.TitleStyle.Render(v.row.key) + "\n")
	if v.row.label != "" {
		b.WriteString(ui.DimStyle.Render(" label ") + v.row.label + "\n")
	}
	b.WriteString("\n")
	section(v.srcName, v.row.src)
	section(v.dstName, v.row.dst)
	return b.String()
}

func (v *compareView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-1))
		v.vp.SetContent(v.content())
		return v, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "g":
			v.vp.GotoTop()
			return v, nil
		case "G":
			v.vp.GotoBottom()
			return v, nil
		}
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v *compareView) View() string { return v.vp.View() }

func (v *compareView) Breadcrumb() string { return v.row.key }

func (v *compareView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{{Keys: "j/k", Desc: "scroll"}}
}
