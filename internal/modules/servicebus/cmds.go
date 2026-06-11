package servicebus

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/GoosieZA/aztui/internal/ui"
)

const (
	adminTimeout = 30 * time.Second
	peekTimeout  = 30 * time.Second
	purgeTimeout = 10 * time.Minute
	peekBatch    = 100
)

type entitiesMsg struct {
	entities []Entity
	err      error
}

type subsMsg struct {
	entities []Entity
	err      error
}

type peekMsg struct {
	msgs []*azservicebus.ReceivedMessage
	more bool // true when this is a "load more" append
	err  error
}

type opDoneMsg struct {
	action string
	err    error
}

// refreshMsg is handed back via ui.PopWith so list views reload counts after
// a child view mutated state (purge, resubmit, send).
type refreshMsg struct{}

type entitySpec struct {
	Kind  string `yaml:"kind"`
	Name  string `yaml:"name"`
	Topic string `yaml:"topic"`
}

func entityTemplate(kind, topic string) []byte {
	return []byte(fmt.Sprintf(`# aztui — new Service Bus entity (created with broker defaults).
# Save and quit to create; quit without saving changes to cancel.
kind: %s   # queue | topic | subscription
name: ""
topic: %q  # only for kind: subscription
`, kind, topic))
}

func loadEntitiesCmd(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		entities, err := c.ListEntities(ctx)
		return entitiesMsg{entities: entities, err: err}
	}
}

func loadSubsCmd(c *Client, topic string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		entities, err := c.ListSubscriptions(ctx, topic)
		return subsMsg{entities: entities, err: err}
	}
}

func peekCmd(c *Client, ent Entity, dlq bool, fromSeq *int64) tea.Cmd {
	more := fromSeq != nil
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), peekTimeout)
		defer cancel()
		msgs, err := c.Peek(ctx, ent, dlq, fromSeq, peekBatch)
		return peekMsg{msgs: msgs, more: more, err: err}
	}
}

func sendCmd(c *Client, ent Entity, spec sendSpec) tea.Cmd {
	return func() tea.Msg {
		msg, err := spec.toMessage()
		if err != nil {
			return opDoneMsg{action: "send", err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		err = c.Send(ctx, ent, msg)
		return opDoneMsg{action: fmt.Sprintf("sent message to %s", ent.SendTarget()), err: err}
	}
}

func purgeCmd(c *Client, ent Entity, dlq bool) tea.Cmd {
	what := ent.Path()
	if dlq {
		what += " DLQ"
	}
	return func() tea.Msg {
		opID := ui.BeginOp("purging %s", what)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), purgeTimeout)
		defer cancel()
		n, err := c.Purge(ctx, ent, dlq)
		return opDoneMsg{action: fmt.Sprintf("purged %d messages from %s", n, what), err: err}
	}
}

func resubmitCmd(c *Client, ent Entity, seq int64) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("resubmitting seq %d", seq)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := c.ResubmitFromDLQ(ctx, ent, seq)
		return opDoneMsg{action: fmt.Sprintf("resubmitted seq %d to %s", seq, ent.SendTarget()), err: err}
	}
}

func deleteEntityCmd(c *Client, ent Entity) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		err := c.DeleteEntity(ctx, ent)
		return opDoneMsg{action: fmt.Sprintf("deleted %s %s", ent.Kind, ent.Path()), err: err}
	}
}

func createEntityCmd(c *Client, raw []byte) tea.Cmd {
	return func() tea.Msg {
		var spec entitySpec
		if err := yaml.Unmarshal(raw, &spec); err != nil {
			return opDoneMsg{action: "create", err: fmt.Errorf("invalid yaml: %w", err)}
		}
		if spec.Name == "" {
			return opDoneMsg{action: "create", err: fmt.Errorf("name is required")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		err := c.CreateEntity(ctx, spec.Kind, spec.Name, spec.Topic)
		return opDoneMsg{action: fmt.Sprintf("created %s %s", spec.Kind, spec.Name), err: err}
	}
}
