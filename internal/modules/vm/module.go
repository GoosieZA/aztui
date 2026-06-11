// Package vm is the aztui module for Azure Virtual Machines: power
// lifecycle (start/stop/restart), VM extensions, gallery applications, and
// one-key SSH into Linux machines.
package vm

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "vm" }
func (module) Aliases() []string { return []string{"vms", "compute"} }
func (module) Title() string     { return "Virtual Machines" }
func (module) Icon() string      { return "🖥" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.compute/virtualmachines"}
}

// clients bundles the ARM clients one VM dashboard needs.
type clients struct {
	vms  *armcompute.VirtualMachinesClient
	exts *armcompute.VirtualMachineExtensionsClient
	nics *armnetwork.InterfacesClient
	pips *armnetwork.PublicIPAddressesClient
}

func newClients(subscriptionID string, cred azcore.TokenCredential) (*clients, error) {
	vms, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	exts, err := armcompute.NewVirtualMachineExtensionsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	nics, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	pips, err := armnetwork.NewPublicIPAddressesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	return &clients{vms: vms, exts: exts, nics: nics, pips: pips}, nil
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	c, err := newClients(res.SubscriptionID, mctx.Cred)
	if err != nil {
		return nil, fmt.Errorf("creating compute clients for %s: %w", res.Name, err)
	}
	return newDashboard(mctx, res, c), nil
}

// armParts extracts the resource group and name from an ARM resource ID.
func armParts(id string) (rg, name string) {
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "resourceGroups") {
			rg = parts[i+1]
		}
	}
	if len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	return rg, name
}

func strFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
