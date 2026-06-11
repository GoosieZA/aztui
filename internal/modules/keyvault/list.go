package keyvault

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

const opTimeout = 30 * time.Second

type secretsMsg struct {
	secrets []*azsecrets.SecretProperties
	err     error
}

// fetchedMsg carries a fully fetched secret (with its value) plus what to do
// with it: open the detail view or seed an $EDITOR session.
type fetchedMsg struct {
	secret  azsecrets.Secret
	purpose string // "view" | "edit"
	err     error
}

type opDoneMsg struct {
	action string
	err    error
}

// refreshMsg is handed back by the detail view after it writes a new version.
type refreshMsg struct{}

type newSpec struct {
	Name        string `yaml:"name"`
	ContentType string `yaml:"content_type"`
	Value       string `yaml:"value"`
}

const newTemplate = `# aztui — new Key Vault secret.
# Save and quit to create; quit without saving changes to cancel.
name: ""
content_type: ""
value: ""
`

type listView struct {
	res    azure.Resource
	client *azsecrets.Client

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool
	loadErr error

	secrets []*azsecrets.SecretProperties
	editing string // secret name with an in-flight $EDITOR session

	width, height int
}

func newListView(res azure.Resource, client *azsecrets.Client) *listView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "NAME", Weight: 6},
		ui.Column{Title: "ENABLED", Width: 7},
		ui.Column{Title: "CONTENT TYPE", Weight: 3},
		ui.Column{Title: "UPDATED", Width: 8},
	)
	t.Empty = "no secrets in this vault (or no list permission)"
	return &listView{res: res, client: client, table: t, spin: sp, loading: true}
}

func (v *listView) Init() tea.Cmd {
	return tea.Batch(v.spin.Tick, v.load())
}

func (v *listView) load() tea.Cmd {
	client := v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		pager := client.NewListSecretPropertiesPager(nil)
		var all []*azsecrets.SecretProperties
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return secretsMsg{err: err}
			}
			all = append(all, page.Value...)
		}
		sort.SliceStable(all, func(i, j int) bool { return ui.NaturalLess(secretName(all[i]), secretName(all[j])) })
		return secretsMsg{secrets: all}
	}
}

func secretName(s *azsecrets.SecretProperties) string {
	if s == nil || s.ID == nil {
		return ""
	}
	return s.ID.Name()
}

func (v *listView) setSecrets(secrets []*azsecrets.SecretProperties) {
	v.secrets = secrets
	rows := make([][]string, len(secrets))
	for i, s := range secrets {
		enabled, updated, contentType := "-", "-", ""
		if s.Attributes != nil {
			if s.Attributes.Enabled != nil {
				enabled = "no"
				if *s.Attributes.Enabled {
					enabled = "yes"
				}
			}
			if s.Attributes.Updated != nil {
				updated = ui.Ago(*s.Attributes.Updated)
			}
		}
		if s.ContentType != nil {
			contentType = *s.ContentType
		}
		rows[i] = []string{secretName(s), enabled, contentType, updated}
	}
	v.table.SetRows(rows)
}

func (v *listView) selectedName() (string, bool) {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.secrets) {
		return "", false
	}
	return secretName(v.secrets[idx]), true
}

// fetch retrieves the latest version of a secret, including its value, then
// either opens the detail view or seeds an editor session.
func (v *listView) fetch(name, purpose string) tea.Cmd {
	client := v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		resp, err := client.GetSecret(ctx, name, "", nil)
		return fetchedMsg{secret: resp.Secret, purpose: purpose, err: err}
	}
}

func (v *listView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case secretsMsg:
		v.loading = false
		v.loadErr = msg.err
		if msg.err != nil {
			if azure.IsForbidden(msg.err) {
				return v, ui.Warnf("no data-plane access to %s", v.res.Name)
			}
			return v, ui.Err(msg.err)
		}
		v.setSecrets(msg.secrets)
		return v, nil

	case fetchedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Errorf("fetching secret: %v", msg.err)
		}
		switch msg.purpose {
		case "view":
			return v, ui.Push(newDetailView(v.client, msg.secret))
		case "copy":
			value := ""
			if msg.secret.Value != nil {
				value = *msg.secret.Value
			}
			return v, ui.Yank(msg.secret.ID.Name(), value)
		case "edit":
			v.editing = msg.secret.ID.Name()
			value := ""
			if msg.secret.Value != nil {
				value = *msg.secret.Value
			}
			return v, ui.OpenEditor("edit", []byte(value), "txt")
		}
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

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "delete" {
				return v, deleteCmd(v.client, result.Payload.(string))
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
	case "e", "n", "d":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
	}
	switch key {
	case "enter":
		if name, ok := v.selectedName(); ok {
			v.loading = true
			return tea.Batch(v.spin.Tick, v.fetch(name, "view")), true
		}
	case "e":
		if name, ok := v.selectedName(); ok {
			v.loading = true
			return tea.Batch(v.spin.Tick, v.fetch(name, "edit")), true
		}
	case "y":
		if name, ok := v.selectedName(); ok {
			v.loading = true
			return tea.Batch(v.spin.Tick, v.fetch(name, "copy")), true
		}
	case "n":
		return ui.OpenEditor("new", []byte(newTemplate), "yaml"), true
	case "d":
		if name, ok := v.selectedName(); ok {
			v.confirm.Ask("delete", fmt.Sprintf("Soft-delete secret %q?", name), name)
			return nil, true
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
		name := v.editing
		v.editing = ""
		if name == "" {
			return nil
		}
		return setCmd(v.client, name, strings.TrimSuffix(string(msg.Content), "\n"), nil)
	case "new":
		var spec newSpec
		if err := yaml.Unmarshal(msg.Content, &spec); err != nil {
			return ui.Errorf("invalid yaml: %v", err)
		}
		if spec.Name == "" {
			return ui.Errorf("name is required")
		}
		var contentType *string
		if spec.ContentType != "" {
			contentType = to.Ptr(spec.ContentType)
		}
		return setCmd(v.client, spec.Name, spec.Value, contentType)
	}
	return nil
}

// setCmd writes a secret value — Key Vault versions are immutable, so this
// always creates a new version (or a brand-new secret).
func setCmd(client *azsecrets.Client, name, value string, contentType *string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, err := client.SetSecret(ctx, name, azsecrets.SetSecretParameters{
			Value:       to.Ptr(value),
			ContentType: contentType,
		}, nil)
		return opDoneMsg{action: fmt.Sprintf("set new version of %s", name), err: err}
	}
}

func deleteCmd(client *azsecrets.Client, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, err := client.DeleteSecret(ctx, name, nil)
		return opDoneMsg{action: fmt.Sprintf("soft-deleted %s (recoverable per vault policy)", name), err: err}
	}
}

func (v *listView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d secrets", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" working...")
	}
	if v.loadErr != nil && azure.IsForbidden(v.loadErr) {
		return title + "\n\n" +
			ui.WarnStyle.Render(" 403 — you can see this vault, but not its secrets.") + "\n\n" +
			ui.DimStyle.Render(" Reading secrets needs a data-plane role on the vault itself:\n"+
				" ask for \"Key Vault Secrets User\" (RBAC vaults) or a secrets access\n"+
				" policy. ARM access — seeing the vault in lists — is granted separately.\n\n"+
				" R retries once access is granted.")
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
		{Keys: "enter", Desc: "view secret (value masked)"},
		{Keys: "e", Desc: "new version in $EDITOR"},
		{Keys: "y", Desc: "yank secret value"},
		{Keys: "n", Desc: "new secret"},
		{Keys: "d", Desc: "soft-delete secret"},
		{Keys: "R", Desc: "refresh"},
	}
}
