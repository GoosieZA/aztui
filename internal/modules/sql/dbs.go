package sql

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

const opTimeout = 45 * time.Second

type dbsLoadedMsg struct {
	dbs []*armsql.Database
	err error
}

// refreshMsg is handed back by the scale view so the list reloads.
type refreshMsg struct{}

// dbsView lists one logical server's databases with their SKUs.
type dbsView struct {
	res    azure.Resource // the server
	client *armsql.DatabasesClient

	table   ui.Table
	spin    spinner.Model
	loading bool

	dbs []*armsql.Database

	width, height int
}

func newDBsView(res azure.Resource, client *armsql.DatabasesClient) *dbsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "NAME", Weight: 5},
		ui.Column{Title: "SKU", Width: 12},
		ui.Column{Title: "TIER", Weight: 3},
		ui.Column{Title: "CAPACITY", Width: 10},
		ui.Column{Title: "MAX SIZE", Width: 9},
		ui.Column{Title: "STATUS", Width: 10},
	)
	t.Empty = "no databases on this server"
	return &dbsView{res: res, client: client, table: t, spin: sp, loading: true}
}

func (v *dbsView) Init() tea.Cmd {
	client, rg, server := v.client, v.res.ResourceGroup, v.res.Name
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		pager := client.NewListByServerPager(rg, server, nil)
		var dbs []*armsql.Database
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return dbsLoadedMsg{err: err}
			}
			dbs = append(dbs, page.Value...)
		}
		return dbsLoadedMsg{dbs: dbs}
	})
}

// effectiveSKU prefers the actual current SKU over the requested one.
func effectiveSKU(db *armsql.Database) *armsql.SKU {
	if db.Properties != nil && db.Properties.CurrentSKU != nil {
		return db.Properties.CurrentSKU
	}
	return db.SKU
}

// capacityLabel renders "100 DTU" or "8 vCores" depending on the model.
func capacityLabel(sku *armsql.SKU) string {
	if sku == nil || sku.Capacity == nil {
		return "-"
	}
	n := strconv.FormatInt(int64(*sku.Capacity), 10)
	if isDTUTier(strFrom(sku.Tier)) {
		return n + " DTU"
	}
	return n + " vCores"
}

func (v *dbsView) setDBs(dbs []*armsql.Database) {
	v.dbs = dbs
	rows := make([][]string, len(dbs))
	for i, db := range dbs {
		sku := effectiveSKU(db)
		name, tier := "-", "-"
		if sku != nil {
			name, tier = strFrom(sku.Name), strFrom(sku.Tier)
		}
		status, size := "-", "-"
		if db.Properties != nil {
			if db.Properties.Status != nil {
				status = string(*db.Properties.Status)
			}
			if db.Properties.MaxSizeBytes != nil {
				size = ui.Bytes(*db.Properties.MaxSizeBytes)
			}
		}
		rows[i] = []string{strFrom(db.Name), name, tier, capacityLabel(sku), size, status}
	}
	v.table.SetRows(rows)
}

func (v *dbsView) selected() *armsql.Database {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.dbs) {
		return nil
	}
	return v.dbs[idx]
}

func (v *dbsView) openScale() tea.Cmd {
	db := v.selected()
	if db == nil {
		return nil
	}
	name := strFrom(db.Name)
	if strings.EqualFold(name, "master") {
		return ui.Warnf("the master database cannot be scaled")
	}
	if db.Properties != nil && db.Properties.ElasticPoolID != nil {
		return ui.Warnf("%s is in an elastic pool — scale the pool instead", name)
	}
	sku := effectiveSKU(db)
	if sku != nil && strings.EqualFold(strFrom(sku.Tier), "DataWarehouse") {
		return ui.Warnf("data warehouse SKUs aren't supported here")
	}
	return ui.Push(newScaleView(v.client, v.res, db))
}

func (v *dbsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case dbsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setDBs(msg.dbs)
		return v, nil

	case refreshMsg:
		v.loading = true
		return v, v.Init()

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
			case "enter", "s":
				if cmd := ui.BlockIfReadOnly(); cmd != nil {
					return v, cmd
				}
				return v, v.openScale()
			case "R":
				v.loading = true
				return v, v.Init()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *dbsView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d databases", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" listing databases...")
	}
	return title + "\n" + v.table.View()
}

func (v *dbsView) InputActive() bool { return v.table.InputActive() }

func (v *dbsView) Breadcrumb() string { return v.res.Name }

func (v *dbsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter/s", Desc: "scale database"},
		{Keys: "R", Desc: "refresh"},
	}
}

func strFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
