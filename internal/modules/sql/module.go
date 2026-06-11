// Package sql is the aztui module for Azure SQL: browse servers and their
// databases, and scale databases across the DTU and vCore purchasing models
// with a vi-key slider.
package sql

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "sql" }
func (module) Aliases() []string { return []string{"db", "mssql"} }
func (module) Title() string     { return "SQL Database" }
func (module) Icon() string      { return "🛢" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.sql/servers"}
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	client, err := armsql.NewDatabasesClient(res.SubscriptionID, mctx.Cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating sql client for %s: %w", res.Name, err)
	}
	return newDBsView(res, client), nil
}
