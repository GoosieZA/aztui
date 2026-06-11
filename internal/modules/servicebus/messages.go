package servicebus

import (
	"fmt"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

// messagesView is a non-destructive peek into a queue, subscription, or
// their dead-letter sub-queues.
type messagesView struct {
	client *Client
	ent    Entity
	dlq    bool

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool
	dirty   bool // a mutation happened; tell the parent to refresh on pop

	msgs []*azservicebus.ReceivedMessage

	width, height int
}

func newMessagesView(client *Client, ent Entity, dlq bool) *messagesView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "SEQ", Width: 7},
		ui.Column{Title: "ENQUEUED", Width: 8},
		ui.Column{Title: "MESSAGE ID", Weight: 3},
		ui.Column{Title: "SUBJECT", Weight: 3},
		ui.Column{Title: "DLV", Width: 3},
		ui.Column{Title: "SIZE", Width: 7},
	)
	t.Empty = "no messages"
	return &messagesView{client: client, ent: ent, dlq: dlq, table: t, spin: sp, loading: true}
}

func (v *messagesView) Init() tea.Cmd {
	return tea.Batch(v.spin.Tick, peekCmd(v.client, v.ent, v.dlq, nil))
}

func (v *messagesView) repeek() tea.Cmd {
	v.loading = true
	v.msgs = nil
	return tea.Batch(v.spin.Tick, peekCmd(v.client, v.ent, v.dlq, nil))
}

func (v *messagesView) peekMore() tea.Cmd {
	if len(v.msgs) == 0 {
		return v.repeek()
	}
	last := v.msgs[len(v.msgs)-1].SequenceNumber
	if last == nil {
		return ui.Warnf("missing sequence number — cannot page")
	}
	from := *last + 1
	v.loading = true
	return tea.Batch(v.spin.Tick, peekCmd(v.client, v.ent, v.dlq, &from))
}

func (v *messagesView) setRows() {
	rows := make([][]string, len(v.msgs))
	for i, m := range v.msgs {
		seq := "-"
		if m.SequenceNumber != nil {
			seq = strconv.FormatInt(*m.SequenceNumber, 10)
		}
		enq := "-"
		if m.EnqueuedTime != nil {
			enq = ui.Ago(*m.EnqueuedTime)
		}
		rows[i] = []string{
			seq,
			enq,
			m.MessageID,
			strOf(m.Subject),
			strconv.FormatUint(uint64(m.DeliveryCount), 10),
			ui.Bytes(int64(len(m.Body))),
		}
	}
	v.table.SetRows(rows)
}

func (v *messagesView) selected() (*azservicebus.ReceivedMessage, bool) {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.msgs) {
		return nil, false
	}
	return v.msgs[idx], true
}

func (v *messagesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case peekMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		if msg.more {
			if len(msg.msgs) == 0 {
				return v, ui.Status("no more messages")
			}
			v.msgs = append(v.msgs, msg.msgs...)
		} else {
			v.msgs = msg.msgs
		}
		v.setRows()
		return v, nil

	case opDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("%s failed: %v", msg.action, msg.err)
		}
		ui.RecordChange(v.client.Namespace, msg.action)
		v.dirty = true
		return v, tea.Batch(ui.Status("%s", msg.action), v.repeek())

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
		if msg.Tag == "send" {
			spec, err := parseSpec(msg.Content)
			if err != nil {
				return v, ui.Err(err)
			}
			return v, sendCmd(v.client, v.ent, spec)
		}
		return v, nil

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK {
				switch result.Tag {
				case "purge":
					return v, tea.Batch(ui.Warnf("purging %s...", v.title()), purgeCmd(v.client, v.ent, v.dlq))
				case "resubmit":
					return v, tea.Batch(ui.Warnf("scanning DLQ for seq %d...", result.Payload.(int64)),
						resubmitCmd(v.client, v.ent, result.Payload.(int64)))
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

func (v *messagesView) handleAction(key string) (tea.Cmd, bool) {
	switch key {
	case "s", "c", "r", "P":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd, true
		}
	}
	switch key {
	case "enter":
		if m, ok := v.selected(); ok {
			return ui.Push(newMessageDetail(v.ent, v.dlq, m)), true
		}
	case "m":
		return v.peekMore(), true
	case "t":
		return ui.Push(newTailView(v.client, v.ent, v.dlq)), true
	case "s":
		return ui.OpenEditor("send", sendTemplate(), "json"), true
	case "c":
		if m, ok := v.selected(); ok {
			spec, err := specFromMessage(m)
			if err != nil {
				return ui.Err(err), true
			}
			raw, err := specJSON(spec)
			if err != nil {
				return ui.Err(err), true
			}
			return ui.OpenEditor("send", raw, "json"), true
		}
	case "r":
		if !v.dlq {
			return ui.Warnf("resubmit works from the dead-letter view (x)"), true
		}
		if m, ok := v.selected(); ok && m.SequenceNumber != nil {
			v.confirm.Ask("resubmit",
				fmt.Sprintf("Resubmit seq %d to %q and remove it from the DLQ?", *m.SequenceNumber, v.ent.SendTarget()),
				*m.SequenceNumber)
			return nil, true
		}
	case "P":
		v.confirm.Ask("purge", fmt.Sprintf("Purge ALL messages from %s?", v.title()), nil)
		return nil, true
	case "R":
		return v.repeek(), true
	}
	return nil, false
}

func (v *messagesView) title() string {
	t := v.ent.Path()
	if v.dlq {
		t += " (dead-letter)"
	}
	return t
}

func (v *messagesView) View() string {
	style := ui.TitleStyle
	if v.dlq {
		style = ui.WarnStyle.Bold(true)
	}
	title := style.Render(" "+v.title()) +
		ui.DimStyle.Render(fmt.Sprintf("  %d peeked — m for more", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" peeking messages...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *messagesView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

// PopResult tells the parent list to reload counts when anything was
// mutated from this view (purge, resubmit, send).
func (v *messagesView) PopResult() tea.Msg {
	if v.dirty {
		return refreshMsg{}
	}
	return nil
}

func (v *messagesView) Breadcrumb() string {
	if v.dlq {
		return v.ent.Path() + " (dlq)"
	}
	return v.ent.Path()
}

func (v *messagesView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view message"},
		{Keys: "m", Desc: "peek more"},
		{Keys: "t", Desc: "live-tail this view"},
		{Keys: "s", Desc: "send new message"},
		{Keys: "c", Desc: "clone & edit selected"},
		{Keys: "r", Desc: "resubmit from DLQ"},
		{Keys: "P", Desc: "purge all"},
		{Keys: "R", Desc: "re-peek from start"},
	}
}
