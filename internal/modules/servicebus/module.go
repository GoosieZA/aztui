// Package servicebus is the aztui module for Azure Service Bus namespaces:
// browse queues/topics/subscriptions with live counts, peek active and
// dead-letter messages, send/clone/resubmit, purge, and manage entities —
// the Service Bus Explorer feature set, with vi keys.
package servicebus

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "servicebus" }
func (module) Aliases() []string { return []string{"sb", "bus"} }
func (module) Title() string     { return "Service Bus" }
func (module) Icon() string      { return "🚌" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.servicebus/namespaces"}
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	_, host, err := res.Endpoint()
	if err != nil {
		return nil, err
	}
	client, err := NewClient(host, mctx.Cred)
	if err != nil {
		return nil, err
	}
	return newEntitiesView(res, client), nil
}
