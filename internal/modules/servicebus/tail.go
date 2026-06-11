package servicebus

import (
	"context"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

const (
	tailPollInterval = 2 * time.Second
	tailBuffer       = 500 // ring buffer: oldest rows drop off
)

type tailStartedMsg struct {
	session *TailSession
	next    int64
	err     error
}

type tailBatchMsg struct {
	gen  int
	msgs []*azservicebus.ReceivedMessage
	err  error
}

type tailPollMsg struct{ gen int }

// tailView is a non-destructive live tail: it finds the entity's current end
// sequence, then poll-peeks anything newer, following the bottom like
// `tail -f`.
type tailView struct {
	client *Client
	ent    Entity
	dlq    bool

	session *TailSession
	nextSeq int64
	gen     int // invalidates in-flight polls across pause/resume

	table    ui.Table
	spin     spinner.Model
	starting bool
	paused   bool
	follow   bool

	msgs []*azservicebus.ReceivedMessage

	width, height int
}

func newTailView(client *Client, ent Entity, dlq bool) *tailView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "SEQ", Width: 7},
		ui.Column{Title: "ENQUEUED", Width: 8},
		ui.Column{Title: "MESSAGE ID", Weight: 3},
		ui.Column{Title: "SUBJECT", Weight: 3},
		ui.Column{Title: "SIZE", Width: 7},
	)
	t.Empty = "waiting for new messages..."
	return &tailView{client: client, ent: ent, dlq: dlq, table: t, spin: sp, starting: true, follow: true}
}

func (v *tailView) Init() tea.Cmd {
	client, ent, dlq := v.client, v.ent, v.dlq
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		session, err := client.NewTailSession(ent, dlq)
		if err != nil {
			return tailStartedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		next, err := session.NextSequence(ctx)
		if err != nil {
			session.Close()
			return tailStartedMsg{err: err}
		}
		return tailStartedMsg{session: session, next: next}
	})
}

func (v *tailView) poll() tea.Cmd {
	session, from, gen := v.session, v.nextSeq, v.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msgs, err := session.Peek(ctx, from, peekBatch)
		return tailBatchMsg{gen: gen, msgs: msgs, err: err}
	}
}

func (v *tailView) schedule() tea.Cmd {
	gen := v.gen
	return tea.Tick(tailPollInterval, func(time.Time) tea.Msg { return tailPollMsg{gen: gen} })
}

func (v *tailView) setRows() {
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
		rows[i] = []string{seq, enq, m.MessageID, strOf(m.Subject), ui.Bytes(int64(len(m.Body)))}
	}
	v.table.SetRows(rows)
	if v.follow {
		v.table.GotoBottom()
	}
}

func (v *tailView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case tailStartedMsg:
		v.starting = false
		if msg.err != nil {
			return v, ui.Errorf("starting tail: %v", msg.err)
		}
		v.session = msg.session
		v.nextSeq = msg.next
		return v, v.poll()

	case tailBatchMsg:
		if msg.gen != v.gen || v.session == nil {
			return v, nil // stale poll from before a pause/resume
		}
		if msg.err != nil {
			return v, tea.Batch(ui.Errorf("tail: %v", msg.err), v.schedule())
		}
		if len(msg.msgs) > 0 {
			v.msgs = append(v.msgs, msg.msgs...)
			if len(v.msgs) > tailBuffer {
				v.msgs = v.msgs[len(v.msgs)-tailBuffer:]
			}
			if last := msg.msgs[len(msg.msgs)-1].SequenceNumber; last != nil {
				v.nextSeq = *last + 1
			}
			v.setRows()
		}
		return v, v.schedule()

	case tailPollMsg:
		if msg.gen != v.gen || v.paused || v.session == nil {
			return v, nil
		}
		return v, v.poll()

	case spinner.TickMsg:
		if !v.starting {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		if !v.table.InputActive() {
			switch msg.String() {
			case "enter":
				if idx := v.table.CursorRow(); idx >= 0 && idx < len(v.msgs) {
					return v, ui.Push(newMessageDetail(v.ent, v.dlq, v.msgs[idx]))
				}
				return v, nil
			case "p":
				v.paused = !v.paused
				v.gen++
				if v.paused {
					return v, ui.Warnf("tail paused — p to resume")
				}
				return v, tea.Batch(ui.Status("tail resumed"), v.poll())
			case "f":
				v.follow = true
				v.table.GotoBottom()
				return v, nil
			case "j", "k", "g", "G", "ctrl+u", "ctrl+d", "up", "down":
				v.follow = false
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

// PopResult closes the tail's receiver when the user backs out.
func (v *tailView) PopResult() tea.Msg {
	v.gen++
	if v.session != nil {
		v.session.Close()
		v.session = nil
	}
	return nil
}

func (v *tailView) title() string {
	t := " ⦿ tailing " + v.ent.Path()
	if v.dlq {
		t += " (dead-letter)"
	}
	return t
}

func (v *tailView) View() string {
	style := ui.OKStyle.Bold(true)
	state := ""
	switch {
	case v.paused:
		style = ui.WarnStyle.Bold(true)
		state = "  paused"
	case !v.follow:
		state = "  scrolled (f to follow)"
	}
	title := style.Render(v.title()) +
		ui.DimStyle.Render("  "+strconv.Itoa(len(v.msgs))+" received"+state)
	if v.starting {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" finding the end of the stream...")
	}
	return title + "\n" + v.table.View()
}

func (v *tailView) InputActive() bool { return v.table.InputActive() }

func (v *tailView) Breadcrumb() string { return "tail" }

func (v *tailView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view message"},
		{Keys: "p", Desc: "pause / resume"},
		{Keys: "f", Desc: "follow bottom"},
		{Keys: "j/k", Desc: "scroll (stops following)"},
	}
}
