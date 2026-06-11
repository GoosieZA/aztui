package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

// detailView shows one setting in full, with scrolling and in-place editing.
// After a successful edit it pops back to the list, which then reloads.
type detailView struct {
	res     azure.Resource
	client  *azappconfig.Client
	setting azappconfig.Setting

	vp            viewport.Model
	width, height int
	saving        bool
}

func newDetailView(res azure.Resource, client *azappconfig.Client, s azappconfig.Setting) *detailView {
	return &detailView{res: res, client: client, setting: s}
}

func (v *detailView) Init() tea.Cmd { return nil }

func (v *detailView) content() string {
	s := v.setting
	var b strings.Builder
	field := func(name, value string) {
		b.WriteString(ui.DimStyle.Render(fmt.Sprintf(" %-14s", name)) + value + "\n")
	}
	field("Key", ui.TitleStyle.Render(deref(s.Key)))
	field("Label", deref(s.Label))
	field("Content type", deref(s.ContentType))
	if s.LastModified != nil {
		field("Last modified", s.LastModified.Local().Format("2006-01-02 15:04:05"))
	}
	ro := "no"
	if s.IsReadOnly != nil && *s.IsReadOnly {
		ro = ui.WarnStyle.Render("yes — unlock with L in the list view")
	}
	field("Read-only", ro)
	if s.ETag != nil {
		field("ETag", string(*s.ETag))
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
			field(name, k+"="+s.Tags[k])
		}
	}
	b.WriteString("\n" + ui.TableHeaderStyle.Render(" VALUE") + "\n")
	b.WriteString(prettify(deref(s.Value), deref(s.ContentType)))
	return b.String()
}

// prettify pretty-prints JSON values; everything else passes through.
func prettify(value, contentType string) string {
	trimmed := strings.TrimSpace(value)
	looksJSON := strings.Contains(strings.ToLower(contentType), "json") ||
		strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
	if looksJSON && json.Valid([]byte(trimmed)) {
		var out bytes.Buffer
		if err := json.Indent(&out, []byte(trimmed), "", "  "); err == nil {
			return out.String()
		}
	}
	return value
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
		ui.RecordChange(v.res.Name, msg.action)
		// Hand the refresh hint to the list view underneath.
		return v, tea.Batch(ui.Status("%s", msg.action), ui.PopWith(refreshMsg{}))

	case ui.EditorResult:
		if msg.Err != nil {
			return v, ui.Errorf("editor: %v", msg.Err)
		}
		if msg.Canceled {
			return v, ui.Status("no changes")
		}
		v.saving = true
		return v, saveCmd(v.client, v.setting, strings.TrimSuffix(string(msg.Content), "\n"))

	case tea.KeyMsg:
		switch msg.String() {
		case "e":
			if cmd := ui.BlockIfReadOnly(); cmd != nil {
				return v, cmd
			}
			if v.setting.IsReadOnly != nil && *v.setting.IsReadOnly {
				return v, ui.Warnf("setting is read-only")
			}
			return v, ui.OpenEditor("detail-edit", []byte(deref(v.setting.Value)), extFor(v.setting))
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
	title := ui.TitleStyle.Render(" " + deref(v.setting.Key))
	if v.saving {
		title += ui.DimStyle.Render("  saving...")
	}
	return title + "\n" + v.vp.View()
}

func (v *detailView) Breadcrumb() string { return deref(v.setting.Key) }

func (v *detailView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "e", Desc: "edit value in $EDITOR"},
		{Keys: "j/k", Desc: "scroll"},
	}
}
