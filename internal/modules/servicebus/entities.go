package servicebus

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

// entitiesView lists a namespace's queues and topics with live counts.
type entitiesView struct {
	res    azure.Resource
	client *Client

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	entities []Entity

	width, height int
}

func newEntitiesView(res azure.Resource, client *Client) *entitiesView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "TYPE", Width: 6},
		ui.Column{Title: "NAME", Weight: 6},
		ui.Column{Title: "ACTIVE", Width: 8},
		ui.Column{Title: "DLQ", Width: 8},
		ui.Column{Title: "SCHED", Width: 8},
		ui.Column{Title: "SUBS", Width: 5},
		ui.Column{Title: "STATUS", Width: 9},
	)
	t.Empty = "no queues or topics in this namespace"
	return &entitiesView{res: res, client: client, table: t, spin: sp, loading: true}
}

func (v *entitiesView) Init() tea.Cmd {
	return tea.Batch(v.spin.Tick, loadEntitiesCmd(v.client))
}

func (v *entitiesView) setEntities(entities []Entity) {
	v.entities = entities
	rows := make([][]string, len(entities))
	for i, e := range entities {
		active, dlq, subs := "-", "-", "-"
		if e.Kind == "queue" {
			active = strconv.FormatInt(e.Active, 10)
			dlq = strconv.FormatInt(e.DLQ, 10)
		}
		if e.Kind == "topic" {
			subs = strconv.FormatInt(e.Subs, 10)
		}
		rows[i] = []string{e.Kind, e.Name, active, dlq, strconv.FormatInt(e.Scheduled, 10), subs, e.Status}
	}
	v.table.SetRows(rows)
}

func (v *entitiesView) selected() (Entity, bool) {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.entities) {
		return Entity{}, false
	}
	return v.entities[idx], true
}

func (v *entitiesView) reload() tea.Cmd {
	v.loading = true
	return tea.Batch(v.spin.Tick, loadEntitiesCmd(v.client))
}

func (v *entitiesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case entitiesMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setEntities(msg.entities)
		return v, nil

	case refreshMsg:
		return v, v.reload()

	case opDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange(v.res.Name, msg.action)
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
			if ent, ok := v.selected(); ok {
				return v, v.parseAndSend(ent, msg.Content)
			}
		}
		return v, nil

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK {
				switch result.Tag {
				case "delete":
					return v, deleteEntityCmd(v.client, result.Payload.(Entity))
				case "purge":
					return v, tea.Batch(ui.Warnf("purging %s...", result.Payload.(Entity).Name),
						purgeCmd(v.client, result.Payload.(Entity), false))
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

func (v *entitiesView) handleAction(key string) (tea.Cmd, bool) {
	switch key {
	case "s", "n", "D", "P":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
	}
	ent, ok := v.selected()
	switch key {
	case "enter":
		if !ok {
			return nil, true
		}
		if ent.Kind == "topic" {
			return ui.Push(newSubsView(v.client, ent)), true
		}
		return ui.Push(newMessagesView(v.client, ent, false)), true
	case "x":
		if ok && ent.Kind == "queue" {
			return ui.Push(newMessagesView(v.client, ent, true)), true
		}
		return ui.Warnf("dead-letter queues live on queues and subscriptions"), true
	case "T":
		if ok && ent.Kind == "queue" {
			return ui.Push(newTailView(v.client, ent, false)), true
		}
		return ui.Warnf("tail queues here; tail subscriptions from inside the topic"), true
	case "s":
		if ok {
			return ui.OpenEditor("send", sendTemplate(), "json"), true
		}
	case "n":
		return ui.OpenEditor("new-entity", entityTemplate("queue", ""), "yaml"), true
	case "D":
		if ok {
			v.confirm.Ask("delete", fmt.Sprintf("Delete %s %q? This cannot be undone.", ent.Kind, ent.Name), ent)
			return nil, true
		}
	case "P":
		if ok && ent.Kind == "queue" {
			v.confirm.Ask("purge", fmt.Sprintf("Purge %d active messages from %q?", ent.Active, ent.Name), ent)
			return nil, true
		}
		return ui.Warnf("purge queues here; purge subscriptions from inside the topic"), true
	case "R":
		return v.reload(), true
	}
	return nil, false
}

func (v *entitiesView) parseAndSend(ent Entity, raw []byte) tea.Cmd {
	spec, err := parseSpec(raw)
	if err != nil {
		return ui.Err(err)
	}
	return sendCmd(v.client, ent, spec)
}

func (v *entitiesView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d entities", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" loading queues and topics...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *entitiesView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *entitiesView) Breadcrumb() string { return v.res.Name }

func (v *entitiesView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "peek queue / open topic"},
		{Keys: "x", Desc: "peek dead-letter queue"},
		{Keys: "T", Desc: "live-tail queue"},
		{Keys: "s", Desc: "send message"},
		{Keys: "n", Desc: "new queue/topic/subscription"},
		{Keys: "D", Desc: "delete entity"},
		{Keys: "P", Desc: "purge queue"},
		{Keys: "R", Desc: "refresh"},
	}
}
