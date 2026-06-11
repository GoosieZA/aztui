package servicebus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

// messageDetail shows one peeked message in full.
type messageDetail struct {
	ent Entity
	dlq bool
	msg *azservicebus.ReceivedMessage

	vp            viewport.Model
	width, height int
}

func newMessageDetail(ent Entity, dlq bool, msg *azservicebus.ReceivedMessage) *messageDetail {
	return &messageDetail{ent: ent, dlq: dlq, msg: msg}
}

func (v *messageDetail) Init() tea.Cmd { return nil }

func (v *messageDetail) content() string {
	m := v.msg
	var b strings.Builder
	field := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(ui.DimStyle.Render(fmt.Sprintf(" %-22s", name)) + value + "\n")
	}

	field("Message ID", m.MessageID)
	if m.SequenceNumber != nil {
		field("Sequence number", fmt.Sprintf("%d", *m.SequenceNumber))
	}
	field("Subject", strOf(m.Subject))
	field("Content type", strOf(m.ContentType))
	field("Correlation ID", strOf(m.CorrelationID))
	field("Session ID", strOf(m.SessionID))
	field("To", strOf(m.To))
	field("Reply to", strOf(m.ReplyTo))
	field("Partition key", strOf(m.PartitionKey))
	if m.EnqueuedTime != nil {
		field("Enqueued", m.EnqueuedTime.Local().Format("2006-01-02 15:04:05"))
	}
	if m.ExpiresAt != nil {
		field("Expires", m.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	}
	field("Delivery count", fmt.Sprintf("%d", m.DeliveryCount))
	field("State", fmt.Sprintf("%v", m.State))

	if v.dlq {
		b.WriteString("\n" + ui.WarnStyle.Bold(true).Render(" DEAD-LETTER INFO") + "\n")
		field("Reason", strOf(m.DeadLetterReason))
		field("Description", strOf(m.DeadLetterErrorDescription))
		field("Source", strOf(m.DeadLetterSource))
	}

	if len(m.ApplicationProperties) > 0 {
		b.WriteString("\n" + ui.TableHeaderStyle.Render(" APPLICATION PROPERTIES") + "\n")
		keys := make([]string, 0, len(m.ApplicationProperties))
		for k := range m.ApplicationProperties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			field(k, fmt.Sprintf("%v", m.ApplicationProperties[k]))
		}
	}

	b.WriteString("\n" + ui.TableHeaderStyle.Render(" BODY") + ui.DimStyle.Render(fmt.Sprintf("  %s", ui.Bytes(int64(len(m.Body))))) + "\n")
	body := ui.PreviewBody(m.Body)
	if json.Valid(m.Body) {
		var out bytes.Buffer
		if err := json.Indent(&out, m.Body, "", "  "); err == nil {
			body = out.String()
		}
	}
	b.WriteString(body)
	return b.String()
}

func (v *messageDetail) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-2))
		v.vp.SetContent(v.content())
		return v, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "y":
			return v, ui.Yank("message "+v.msg.MessageID, ui.PreviewBody(v.msg.Body))
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

func (v *messageDetail) View() string {
	title := ui.TitleStyle.Render(" message " + v.msg.MessageID)
	return title + "\n" + v.vp.View()
}

func (v *messageDetail) Breadcrumb() string {
	if seq := v.msg.SequenceNumber; seq != nil {
		return fmt.Sprintf("seq %d", *seq)
	}
	return v.msg.MessageID
}

func (v *messageDetail) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "y", Desc: "yank message body"},
		{Keys: "j/k", Desc: "scroll"},
		{Keys: "g/G", Desc: "top/bottom"},
	}
}
