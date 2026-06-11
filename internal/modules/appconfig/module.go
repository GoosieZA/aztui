// Package appconfig is the aztui module for Azure App Configuration stores:
// browse, filter, view, edit, create, delete, and lock key-values.
package appconfig

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "appconfig" }
func (module) Aliases() []string { return []string{"ac", "appconfiguration"} }
func (module) Title() string     { return "App Configuration" }
func (module) Icon() string      { return "⚙" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.appconfiguration/configurationstores"}
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	endpoint, _, err := res.Endpoint()
	if err != nil {
		return nil, err
	}
	client, err := azappconfig.NewClient(endpoint, mctx.Cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating app configuration client for %s: %w", res.Name, err)
	}
	return newListView(mctx, res, client), nil
}
