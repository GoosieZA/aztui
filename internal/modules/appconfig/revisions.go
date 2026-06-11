package appconfig

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

type revisionsMsg struct {
	revs []azappconfig.Setting
	err  error
}

// revisionsView shows a setting's history (the store keeps 7–30 days of
// revisions) and can roll any of them back — the rollback itself becomes a
// new revision, so it's always reversible.
type revisionsView struct {
	res        azure.Resource
	client     *azappconfig.Client
	key, label string

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	revs       []azappconfig.Setting
	rolledBack bool

	width, height int
}

func newRevisionsView(res azure.Resource, client *azappconfig.Client, key, label string) *revisionsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "WHEN", Width: 17},
		ui.Column{Title: "AGO", Width: 6},
		ui.Column{Title: "VALUE", Weight: 8},
		ui.Column{Title: "CONTENT TYPE", Weight: 3},
		ui.Column{Title: "RO", Width: 2},
	)
	t.Empty = "no revision history (retention is 7 days on free, 30 on standard)"
	return &revisionsView{res: res, client: client, key: key, label: label, table: t, spin: sp, loading: true}
}

func (v *revisionsView) Init() tea.Cmd {
	client, key, label := v.client, v.key, v.label
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		pager := client.NewListRevisionsPager(azappconfig.SettingSelector{KeyFilter: to.Ptr(key)}, nil)
		var revs []azappconfig.Setting
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return revisionsMsg{err: err}
			}
			for _, s := range page.Settings {
				// The selector matches by key only; pin the exact label here.
				if deref(s.Label) == label {
					revs = append(revs, s)
				}
			}
		}
		return revisionsMsg{revs: revs} // service returns newest first
	})
}

func (v *revisionsView) setRevs(revs []azappconfig.Setting) {
	v.revs = revs
	rows := make([][]string, len(revs))
	for i, s := range revs {
		when, ago := "-", "-"
		if s.LastModified != nil {
			when = s.LastModified.Local().Format("2006-01-02 15:04")
			ago = ui.Ago(*s.LastModified)
		}
		ro := ""
		if s.IsReadOnly != nil && *s.IsReadOnly {
			ro = "✓"
		}
		current := ""
		if i == 0 {
			current = " ← current"
		}
		rows[i] = []string{when, ago, displayValue(s) + current, deref(s.ContentType), ro}
	}
	v.table.SetRows(rows)
}

// rollbackCmd writes a past revision's value (and content type) as the new
// current value.
func rollbackCmd(client *azappconfig.Client, rev azappconfig.Setting) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		value := deref(rev.Value)
		_, err := client.SetSetting(ctx, deref(rev.Key), &value, &azappconfig.SetSettingOptions{
			Label:       rev.Label,
			ContentType: rev.ContentType,
		})
		when := ""
		if rev.LastModified != nil {
			when = rev.LastModified.Local().Format("2006-01-02 15:04")
		}
		return opDoneMsg{action: fmt.Sprintf("rolled %s back to revision from %s", deref(rev.Key), when), err: err}
	}
}

func (v *revisionsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case revisionsMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setRevs(msg.revs)
		return v, nil

	case opDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange(v.res.Name, msg.action)
		v.rolledBack = true
		v.loading = true
		return v, tea.Batch(ui.Status("%s", msg.action), v.spin.Tick, v.Init())

	case ui.ActivatedMsg:
		if v.loading {
			return v, v.Init()
		}
		return v, nil

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "rollback" {
				return v, rollbackCmd(v.client, result.Payload.(azappconfig.Setting))
			}
			return v, nil
		}
		if !v.table.InputActive() {
			switch msg.String() {
			case "enter":
				if idx := v.table.CursorRow(); idx >= 0 && idx < len(v.revs) {
					rev := v.revs[idx]
					return v, ui.Push(newValueView(v.res.Name, v.key, &rev))
				}
				return v, nil
			case "y":
				if idx := v.table.CursorRow(); idx >= 0 && idx < len(v.revs) {
					return v, ui.Yank(v.key+" (revision)", deref(v.revs[idx].Value))
				}
				return v, nil
			case "r":
				if cmd := ui.BlockIfReadOnly(); cmd != nil {
					return v, cmd
				}
				idx := v.table.CursorRow()
				if idx < 0 || idx >= len(v.revs) {
					return v, nil
				}
				if idx == 0 {
					return v, ui.Warnf("that's already the current revision")
				}
				rev := v.revs[idx]
				when := "-"
				if rev.LastModified != nil {
					when = rev.LastModified.Local().Format("2006-01-02 15:04")
				}
				v.confirm.Ask("rollback",
					fmt.Sprintf("Roll %q back to the revision from %s? The rollback itself becomes a new revision.", v.key, when),
					rev)
				return v, nil
			case "R":
				v.loading = true
				return v, tea.Batch(v.spin.Tick, v.Init())
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

// PopResult tells the settings list to reload after a rollback.
func (v *revisionsView) PopResult() tea.Msg {
	if v.rolledBack {
		return refreshMsg{}
	}
	return nil
}

func (v *revisionsView) View() string {
	label := ""
	if v.label != "" {
		label = " (label " + v.label + ")"
	}
	title := ui.TitleStyle.Render(" "+v.key) +
		ui.DimStyle.Render(fmt.Sprintf("%s  ·  %d revisions", label, v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" loading history...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *revisionsView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *revisionsView) Breadcrumb() string { return "history" }

func (v *revisionsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view revision"},
		{Keys: "r", Desc: "roll back to this revision"},
		{Keys: "y", Desc: "yank revision value"},
		{Keys: "R", Desc: "refresh"},
	}
}
