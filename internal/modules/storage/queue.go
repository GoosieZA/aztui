package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

type queuePeekMsg struct {
	msgs []*azqueue.PeekedMessage
	err  error
}

// queueView peeks a storage queue non-destructively. The service caps peeks
// at 32 messages.
type queueView struct {
	svc  *azqueue.ServiceClient
	name string

	table   ui.Table
	spin    spinner.Model
	loading bool

	msgs []*azqueue.PeekedMessage

	width, height int
}

func newQueueView(svc *azqueue.ServiceClient, name string) *queueView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "MESSAGE ID", Weight: 3},
		ui.Column{Title: "INSERTED", Width: 9},
		ui.Column{Title: "DEQ", Width: 4},
		ui.Column{Title: "TEXT", Weight: 6},
	)
	t.Empty = "no messages"
	return &queueView{svc: svc, name: name, table: t, spin: sp, loading: true}
}

func (v *queueView) Init() tea.Cmd {
	svc, name := v.svc, v.name
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		qc := svc.NewQueueClient(name)
		resp, err := qc.PeekMessages(ctx, &azqueue.PeekMessagesOptions{NumberOfMessages: to.Ptr[int32](32)})
		if err != nil {
			return queuePeekMsg{err: err}
		}
		return queuePeekMsg{msgs: resp.Messages}
	})
}

// decodeText makes queue payloads readable: most producers base64-encode
// them, so try that first, falling back to the raw text.
func decodeText(raw string) (string, bool) {
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil && utf8.Valid(data) {
		return string(data), true
	}
	return raw, false
}

func (v *queueView) setRows() {
	rows := make([][]string, len(v.msgs))
	for i, m := range v.msgs {
		text, _ := decodeText(deref(m.MessageText))
		inserted := "-"
		if m.InsertionTime != nil {
			inserted = ui.Ago(*m.InsertionTime)
		}
		deq := "-"
		if m.DequeueCount != nil {
			deq = strconv.FormatInt(*m.DequeueCount, 10)
		}
		rows[i] = []string{deref(m.MessageID), inserted, deq, text}
	}
	v.table.SetRows(rows)
}

func (v *queueView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case queuePeekMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.msgs = msg.msgs
		v.setRows()
		return v, nil

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
		if !v.table.InputActive() {
			switch msg.String() {
			case "enter":
				idx := v.table.CursorRow()
				if idx >= 0 && idx < len(v.msgs) {
					return v, ui.Push(newQueueMsgDetail(v.name, v.msgs[idx]))
				}
				return v, nil
			case "R":
				v.loading = true
				return v, v.Init()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *queueView) View() string {
	title := ui.TitleStyle.Render(" "+v.name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d peeked (service caps peeks at 32)", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" peeking messages...")
	}
	return title + "\n" + v.table.View()
}

func (v *queueView) InputActive() bool { return v.table.InputActive() }

func (v *queueView) Breadcrumb() string { return v.name }

func (v *queueView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view message"},
		{Keys: "R", Desc: "re-peek"},
	}
}

// --- message detail ----------------------------------------------------------

type queueMsgDetail struct {
	queue string
	msg   *azqueue.PeekedMessage

	vp            viewport.Model
	width, height int
}

func newQueueMsgDetail(queue string, msg *azqueue.PeekedMessage) *queueMsgDetail {
	return &queueMsgDetail{queue: queue, msg: msg}
}

func (v *queueMsgDetail) Init() tea.Cmd { return nil }

func (v *queueMsgDetail) content() string {
	m := v.msg
	var b strings.Builder
	field := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(ui.DimStyle.Render(fmt.Sprintf(" %-16s", name)) + value + "\n")
	}
	field("Message ID", deref(m.MessageID))
	if m.InsertionTime != nil {
		field("Inserted", m.InsertionTime.Local().Format("2006-01-02 15:04:05"))
	}
	if m.ExpirationTime != nil {
		field("Expires", m.ExpirationTime.Local().Format("2006-01-02 15:04:05"))
	}
	if m.DequeueCount != nil {
		field("Dequeue count", strconv.FormatInt(*m.DequeueCount, 10))
	}

	text, decoded := decodeText(deref(m.MessageText))
	label := " TEXT"
	if decoded {
		label = " TEXT (base64-decoded)"
	}
	b.WriteString("\n" + ui.TableHeaderStyle.Render(label) + "\n")
	if json.Valid([]byte(text)) {
		var out bytes.Buffer
		if err := json.Indent(&out, []byte(text), "", "  "); err == nil {
			text = out.String()
		}
	}
	b.WriteString(text)
	return b.String()
}

func (v *queueMsgDetail) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (v *queueMsgDetail) View() string { return v.vp.View() }

func (v *queueMsgDetail) Breadcrumb() string { return deref(v.msg.MessageID) }

func (v *queueMsgDetail) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{{Keys: "j/k", Desc: "scroll"}}
}
