package keyvault

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

// detailView shows one secret. The value starts masked — v reveals it.
type detailView struct {
	client *azsecrets.Client
	secret azsecrets.Secret

	revealed bool
	saving   bool

	vp            viewport.Model
	width, height int
}

func newDetailView(client *azsecrets.Client, secret azsecrets.Secret) *detailView {
	return &detailView{client: client, secret: secret}
}

func (v *detailView) Init() tea.Cmd { return nil }

func (v *detailView) name() string {
	if v.secret.ID == nil {
		return ""
	}
	return v.secret.ID.Name()
}

func (v *detailView) content() string {
	s := v.secret
	var b strings.Builder
	field := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(ui.DimStyle.Render(fmt.Sprintf(" %-14s", name)) + value + "\n")
	}
	field("Name", ui.TitleStyle.Render(v.name()))
	if s.ID != nil {
		field("Version", s.ID.Version())
	}
	if s.ContentType != nil {
		field("Content type", *s.ContentType)
	}
	if s.Attributes != nil {
		if s.Attributes.Enabled != nil {
			enabled := "no"
			if *s.Attributes.Enabled {
				enabled = "yes"
			}
			field("Enabled", enabled)
		}
		if s.Attributes.Created != nil {
			field("Created", s.Attributes.Created.Local().Format("2006-01-02 15:04:05"))
		}
		if s.Attributes.Updated != nil {
			field("Updated", s.Attributes.Updated.Local().Format("2006-01-02 15:04:05"))
		}
		if s.Attributes.Expires != nil {
			field("Expires", ui.WarnStyle.Render(s.Attributes.Expires.Local().Format("2006-01-02 15:04:05")))
		}
	}
	if len(s.Tags) > 0 {
		keys := make([]string, 0, len(s.Tags))
		for k := range s.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			name := ""
			if i == 0 {
				name = "Tags"
			}
			val := ""
			if s.Tags[k] != nil {
				val = *s.Tags[k]
			}
			field(name, k+"="+val)
		}
	}

	b.WriteString("\n" + ui.TableHeaderStyle.Render(" VALUE"))
	if v.revealed {
		b.WriteString(ui.DimStyle.Render("  v to mask") + "\n")
		if s.Value != nil {
			b.WriteString(*s.Value)
		}
	} else {
		b.WriteString(ui.DimStyle.Render("  v to reveal") + "\n")
		b.WriteString(ui.DimStyle.Render(strings.Repeat("●", 24)))
	}
	return b.String()
}

func (v *detailView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-2))
		v.vp.SetContent(v.content())
		return v, nil

	case opDoneMsg:
		v.saving = false
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange("key vault", msg.action)
		return v, tea.Batch(ui.Status("%s", msg.action), ui.PopWith(refreshMsg{}))

	case ui.EditorResult:
		if msg.Err != nil {
			return v, ui.Errorf("editor: %v", msg.Err)
		}
		if msg.Canceled {
			return v, ui.Status("no changes")
		}
		v.saving = true
		return v, setCmd(v.client, v.name(), strings.TrimSuffix(string(msg.Content), "\n"), v.secret.ContentType)

	case tea.KeyMsg:
		switch msg.String() {
		case "v":
			v.revealed = !v.revealed
			v.vp.SetContent(v.content())
			return v, nil
		case "y":
			value := ""
			if v.secret.Value != nil {
				value = *v.secret.Value
			}
			return v, ui.Yank(v.name(), value)
		case "e":
			if cmd := ui.BlockIfReadOnly(); cmd != nil {
				return v, cmd
			}
			value := ""
			if v.secret.Value != nil {
				value = *v.secret.Value
			}
			return v, ui.OpenEditor("kv-detail-edit", []byte(value), "txt")
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

func (v *detailView) View() string {
	title := ui.TitleStyle.Render(" " + v.name())
	if v.saving {
		title += ui.DimStyle.Render("  saving...")
	}
	return title + "\n" + v.vp.View()
}

func (v *detailView) Breadcrumb() string { return v.name() }

func (v *detailView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "v", Desc: "reveal / mask value"},
		{Keys: "y", Desc: "yank secret value"},
		{Keys: "e", Desc: "new version in $EDITOR"},
		{Keys: "j/k", Desc: "scroll"},
	}
}
