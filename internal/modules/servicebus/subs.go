package servicebus

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

// subsView lists a topic's subscriptions.
type subsView struct {
	client *Client
	topic  Entity

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	subs []Entity

	width, height int
}

func newSubsView(client *Client, topic Entity) *subsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "SUBSCRIPTION", Weight: 6},
		ui.Column{Title: "ACTIVE", Width: 8},
		ui.Column{Title: "DLQ", Width: 8},
		ui.Column{Title: "TOTAL", Width: 8},
		ui.Column{Title: "STATUS", Width: 9},
	)
	t.Empty = "no subscriptions on this topic"
	return &subsView{client: client, topic: topic, table: t, spin: sp, loading: true}
}

func (v *subsView) Init() tea.Cmd {
	return tea.Batch(v.spin.Tick, loadSubsCmd(v.client, v.topic.Name))
}

func (v *subsView) reload() tea.Cmd {
	v.loading = true
	return tea.Batch(v.spin.Tick, loadSubsCmd(v.client, v.topic.Name))
}

func (v *subsView) setSubs(subs []Entity) {
	v.subs = subs
	rows := make([][]string, len(subs))
	for i, s := range subs {
		rows[i] = []string{
			s.Name,
			strconv.FormatInt(s.Active, 10),
			strconv.FormatInt(s.DLQ, 10),
			strconv.FormatInt(s.Total, 10),
			s.Status,
		}
	}
	v.table.SetRows(rows)
}

func (v *subsView) selected() (Entity, bool) {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.subs) {
		return Entity{}, false
	}
	return v.subs[idx], true
}

func (v *subsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case subsMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setSubs(msg.entities)
		return v, nil

	case refreshMsg:
		return v, v.reload()

	case opDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange(v.client.Namespace, msg.action)
		return v, tea.Batch(ui.Status("%s", msg.action), v.reload())

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case ui.EditorResult:
		if msg.Err != nil {
			return v, ui.Errorf("editor: %v", msg.Err)
		}
		if msg.Canceled {
			return v, ui.Status("no changes")
		}
		switch msg.Tag {
		case "new-entity":
			return v, createEntityCmd(v.client, msg.Content)
		case "send":
			spec, err := parseSpec(msg.Content)
			if err != nil {
				return v, ui.Err(err)
			}
			return v, sendCmd(v.client, v.topic, spec)
		}
		return v, nil

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK {
				switch result.Tag {
				case "delete":
					return v, deleteEntityCmd(v.client, result.Payload.(Entity))
				case "purge":
					ent := result.Payload.(Entity)
					return v, tea.Batch(ui.Warnf("purging %s...", ent.Path()), purgeCmd(v.client, ent, false))
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

func (v *subsView) handleAction(key string) (tea.Cmd, bool) {
	switch key {
	case "s", "n", "D", "P":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
	}
	sub, ok := v.selected()
	switch key {
	case "enter":
		if ok {
			return ui.Push(newMessagesView(v.client, sub, false)), true
		}
	case "x":
		if ok {
			return ui.Push(newMessagesView(v.client, sub, true)), true
		}
	case "T":
		if ok {
			return ui.Push(newTailView(v.client, sub, false)), true
		}
	case "s":
		return ui.OpenEditor("send", sendTemplate(), "json"), true
	case "n":
		return ui.OpenEditor("new-entity", entityTemplate("subscription", v.topic.Name), "yaml"), true
	case "D":
		if ok {
			v.confirm.Ask("delete", fmt.Sprintf("Delete subscription %q? This cannot be undone.", sub.Path()), sub)
			return nil, true
		}
	case "P":
		if ok {
			v.confirm.Ask("purge", fmt.Sprintf("Purge %d active messages from %q?", sub.Active, sub.Path()), sub)
			return nil, true
		}
	case "R":
		return v.reload(), true
	}
	return nil, false
}

func (v *subsView) View() string {
	title := ui.TitleStyle.Render(" "+v.topic.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  topic — %d subscriptions", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" loading subscriptions...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *subsView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *subsView) Breadcrumb() string { return v.topic.Name }

func (v *subsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "peek subscription"},
		{Keys: "x", Desc: "peek dead-letter queue"},
		{Keys: "T", Desc: "live-tail subscription"},
		{Keys: "s", Desc: "send message to topic"},
		{Keys: "n", Desc: "new subscription"},
		{Keys: "D", Desc: "delete subscription"},
		{Keys: "P", Desc: "purge subscription"},
		{Keys: "R", Desc: "refresh"},
	}
}
