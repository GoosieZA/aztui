// Package keyvault is the aztui module for Azure Key Vault secrets: browse,
// reveal, create new versions, add, and (soft-)delete secrets.
package keyvault

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "keyvault" }
func (module) Aliases() []string { return []string{"kv"} }
func (module) Title() string     { return "Key Vault" }
func (module) Icon() string      { return "🔑" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.keyvault/vaults"}
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	endpoint, _, err := res.Endpoint()
	if err != nil {
		return nil, err
	}
	client, err := azsecrets.NewClient(endpoint, mctx.Cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating key vault client for %s: %w", res.Name, err)
	}
	return newListView(res, client), nil
}
