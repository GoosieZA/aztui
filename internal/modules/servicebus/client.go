package servicebus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
)

// resubmitScanLimit caps how many DLQ messages a resubmit will receive-and-
// scan while hunting for the target sequence number. Locks on scanned
// messages are abandoned afterwards.
const resubmitScanLimit = 1000

// Client bundles the Service Bus data-plane and management-plane clients for
// one namespace.
type Client struct {
	Namespace string
	sb        *azservicebus.Client
	admin     *admin.Client
}

func NewClient(namespace string, cred azcore.TokenCredential) (*Client, error) {
	sb, err := azservicebus.NewClient(namespace, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating service bus client: %w", err)
	}
	adm, err := admin.NewClient(namespace, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating service bus admin client: %w", err)
	}
	return &Client{Namespace: namespace, sb: sb, admin: adm}, nil
}

// Entity is a queue, topic, or subscription with its runtime counters.
type Entity struct {
	Kind      string // "queue" | "topic" | "subscription"
	Name      string // entity name; for subscriptions this is the subscription name
	Topic     string // parent topic, subscriptions only
	Active    int64
	DLQ       int64
	Scheduled int64
	Total     int64
	Subs      int64 // topics only
	Status    string
}

// Path is the display path, e.g. "orders" or "events/billing-sub".
func (e Entity) Path() string {
	if e.Kind == "subscription" {
		return e.Topic + "/" + e.Name
	}
	return e.Name
}

// CanPeek reports whether messages can be peeked directly (topics can't —
// their messages live in subscriptions).
func (e Entity) CanPeek() bool { return e.Kind != "topic" }

// SendTarget is the queue or topic that new messages should be sent to.
func (e Entity) SendTarget() string {
	if e.Kind == "subscription" {
		return e.Topic
	}
	return e.Name
}

// ListEntities returns all queues and topics with counts and status.
func (c *Client) ListEntities(ctx context.Context) ([]Entity, error) {
	var entities []Entity

	queues := map[string]*Entity{}
	qrPager := c.admin.NewListQueuesRuntimePropertiesPager(nil)
	for qrPager.More() {
		page, err := qrPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing queues: %w", err)
		}
		for _, q := range page.QueueRuntimeProperties {
			queues[q.QueueName] = &Entity{
				Kind:      "queue",
				Name:      q.QueueName,
				Active:    int64(q.ActiveMessageCount),
				DLQ:       int64(q.DeadLetterMessageCount),
				Scheduled: int64(q.ScheduledMessageCount),
				Total:     q.TotalMessageCount,
			}
		}
	}
	qPager := c.admin.NewListQueuesPager(nil)
	for qPager.More() {
		page, err := qPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing queue properties: %w", err)
		}
		for _, q := range page.Queues {
			if e, ok := queues[q.QueueName]; ok && q.Status != nil {
				e.Status = string(*q.Status)
			}
		}
	}
	for _, e := range queues {
		entities = append(entities, *e)
	}

	topics := map[string]*Entity{}
	trPager := c.admin.NewListTopicsRuntimePropertiesPager(nil)
	for trPager.More() {
		page, err := trPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing topics: %w", err)
		}
		for _, t := range page.TopicRuntimeProperties {
			topics[t.TopicName] = &Entity{
				Kind:      "topic",
				Name:      t.TopicName,
				Scheduled: int64(t.ScheduledMessageCount),
				Subs:      int64(t.SubscriptionCount),
			}
		}
	}
	tPager := c.admin.NewListTopicsPager(nil)
	for tPager.More() {
		page, err := tPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing topic properties: %w", err)
		}
		for _, t := range page.Topics {
			if e, ok := topics[t.TopicName]; ok && t.Status != nil {
				e.Status = string(*t.Status)
			}
		}
	}
	for _, e := range topics {
		entities = append(entities, *e)
	}

	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Kind != entities[j].Kind {
			return entities[i].Kind < entities[j].Kind // queues before topics
		}
		return entities[i].Name < entities[j].Name
	})
	return entities, nil
}

// ListSubscriptions returns a topic's subscriptions with counts and status.
func (c *Client) ListSubscriptions(ctx context.Context, topic string) ([]Entity, error) {
	subs := map[string]*Entity{}
	rPager := c.admin.NewListSubscriptionsRuntimePropertiesPager(topic, nil)
	for rPager.More() {
		page, err := rPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing subscriptions for %s: %w", topic, err)
		}
		for _, s := range page.SubscriptionRuntimeProperties {
			subs[s.SubscriptionName] = &Entity{
				Kind:   "subscription",
				Name:   s.SubscriptionName,
				Topic:  topic,
				Active: int64(s.ActiveMessageCount),
				DLQ:    int64(s.DeadLetterMessageCount),
				Total:  s.TotalMessageCount,
			}
		}
	}
	pPager := c.admin.NewListSubscriptionsPager(topic, nil)
	for pPager.More() {
		page, err := pPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing subscription properties for %s: %w", topic, err)
		}
		for _, s := range page.Subscriptions {
			if e, ok := subs[s.SubscriptionName]; ok && s.Status != nil {
				e.Status = string(*s.Status)
			}
		}
	}
	var entities []Entity
	for _, e := range subs {
		entities = append(entities, *e)
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].Name < entities[j].Name })
	return entities, nil
}

func (c *Client) receiver(ent Entity, dlq bool, mode azservicebus.ReceiveMode) (*azservicebus.Receiver, error) {
	opts := &azservicebus.ReceiverOptions{ReceiveMode: mode}
	if dlq {
		opts.SubQueue = azservicebus.SubQueueDeadLetter
	}
	switch ent.Kind {
	case "queue":
		return c.sb.NewReceiverForQueue(ent.Name, opts)
	case "subscription":
		return c.sb.NewReceiverForSubscription(ent.Topic, ent.Name, opts)
	default:
		return nil, fmt.Errorf("cannot receive from a %s — peek one of its subscriptions", ent.Kind)
	}
}

// Peek reads up to count messages non-destructively, optionally from the
// dead-letter sub-queue, starting at fromSeq when given.
func (c *Client) Peek(ctx context.Context, ent Entity, dlq bool, fromSeq *int64, count int) ([]*azservicebus.ReceivedMessage, error) {
	recv, err := c.receiver(ent, dlq, azservicebus.ReceiveModePeekLock)
	if err != nil {
		return nil, err
	}
	defer recv.Close(context.Background())
	return recv.PeekMessages(ctx, count, &azservicebus.PeekMessagesOptions{FromSequenceNumber: fromSeq})
}

// Send delivers one message to the entity's queue or topic.
func (c *Client) Send(ctx context.Context, ent Entity, msg *azservicebus.Message) error {
	sender, err := c.sb.NewSender(ent.SendTarget(), nil)
	if err != nil {
		return err
	}
	defer sender.Close(context.Background())
	return sender.SendMessage(ctx, msg, nil)
}

// Purge drains all messages from the entity (or its DLQ) with
// receive-and-delete, returning how many were removed.
func (c *Client) Purge(ctx context.Context, ent Entity, dlq bool) (int, error) {
	recv, err := c.receiver(ent, dlq, azservicebus.ReceiveModeReceiveAndDelete)
	if err != nil {
		return 0, err
	}
	defer recv.Close(context.Background())

	total := 0
	for {
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		msgs, err := recv.ReceiveMessages(batchCtx, 250, nil)
		cancel()
		total += len(msgs)
		if err != nil {
			// A deadline with nothing received means the entity is drained.
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return total, nil
			}
			return total, err
		}
		if len(msgs) == 0 {
			return total, nil
		}
	}
}

// ResubmitFromDLQ finds the dead-lettered message with the given sequence
// number, re-sends a copy to the main entity, and completes (removes) the
// original from the DLQ. Other messages received while scanning are abandoned
// so they return to the DLQ.
func (c *Client) ResubmitFromDLQ(ctx context.Context, ent Entity, seq int64) error {
	recv, err := c.receiver(ent, true, azservicebus.ReceiveModePeekLock)
	if err != nil {
		return err
	}
	defer recv.Close(context.Background())
	sender, err := c.sb.NewSender(ent.SendTarget(), nil)
	if err != nil {
		return err
	}
	defer sender.Close(context.Background())

	var held []*azservicebus.ReceivedMessage
	defer func() {
		for _, m := range held {
			_ = recv.AbandonMessage(context.Background(), m, nil)
		}
	}()

	scanned := 0
	for scanned < resubmitScanLimit {
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		msgs, err := recv.ReceiveMessages(batchCtx, 50, nil)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				break
			}
			return err
		}
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			if m.SequenceNumber == nil || *m.SequenceNumber != seq {
				held = append(held, m)
				scanned++
				continue
			}
			if err := sender.SendMessage(ctx, m.Message(), nil); err != nil {
				held = append(held, m)
				return fmt.Errorf("re-sending message: %w", err)
			}
			if err := recv.CompleteMessage(ctx, m, nil); err != nil {
				return fmt.Errorf("message was re-sent but removing it from the DLQ failed (it may appear twice): %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("sequence %d not found in the first %d DLQ messages", seq, scanned)
}

// TailSession holds one receiver open for a live tail, so polling doesn't
// pay AMQP link setup on every tick.
type TailSession struct {
	recv *azservicebus.Receiver
}

func (c *Client) NewTailSession(ent Entity, dlq bool) (*TailSession, error) {
	recv, err := c.receiver(ent, dlq, azservicebus.ReceiveModePeekLock)
	if err != nil {
		return nil, err
	}
	return &TailSession{recv: recv}, nil
}

func (t *TailSession) Close() {
	_ = t.recv.Close(context.Background())
}

// Peek reads up to count messages non-destructively from the session.
func (t *TailSession) Peek(ctx context.Context, fromSeq int64, count int) ([]*azservicebus.ReceivedMessage, error) {
	return t.recv.PeekMessages(ctx, count, &azservicebus.PeekMessagesOptions{FromSequenceNumber: &fromSeq})
}

// NextSequence finds the sequence number just past the newest message in the
// entity, so a tail can start "from now". Sequence numbers are monotonically
// increasing per entity, so binary search over single-message peeks finds the
// end in a few dozen round-trips regardless of depth; every non-empty probe
// jumps straight past a real message, so in practice it's far fewer.
func (t *TailSession) NextSequence(ctx context.Context) (int64, error) {
	var low, high int64 = 0, math.MaxInt64 / 2
	for low < high {
		mid := low + (high-low)/2
		msgs, err := t.Peek(ctx, mid, 1)
		if err != nil {
			return 0, err
		}
		if len(msgs) == 0 {
			high = mid
			continue
		}
		seq := msgs[0].SequenceNumber
		if seq == nil {
			return 0, fmt.Errorf("peeked message has no sequence number")
		}
		low = *seq + 1
	}
	return low, nil
}

// CreateEntity makes a new queue, topic, or subscription with defaults.
func (c *Client) CreateEntity(ctx context.Context, kind, name, topic string) error {
	switch kind {
	case "queue":
		_, err := c.admin.CreateQueue(ctx, name, nil)
		return err
	case "topic":
		_, err := c.admin.CreateTopic(ctx, name, nil)
		return err
	case "subscription":
		if topic == "" {
			return fmt.Errorf("subscriptions need a topic")
		}
		_, err := c.admin.CreateSubscription(ctx, topic, name, nil)
		return err
	default:
		return fmt.Errorf("unknown entity kind %q (want queue, topic, or subscription)", kind)
	}
}

// DeleteEntity removes a queue, topic, or subscription.
func (c *Client) DeleteEntity(ctx context.Context, ent Entity) error {
	switch ent.Kind {
	case "queue":
		_, err := c.admin.DeleteQueue(ctx, ent.Name, nil)
		return err
	case "topic":
		_, err := c.admin.DeleteTopic(ctx, ent.Name, nil)
		return err
	case "subscription":
		_, err := c.admin.DeleteSubscription(ctx, ent.Topic, ent.Name, nil)
		return err
	default:
		return fmt.Errorf("unknown entity kind %q", ent.Kind)
	}
}
