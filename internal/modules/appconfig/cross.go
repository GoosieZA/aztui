package appconfig

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

// crossRow is one store's answer for the key being compared.
type crossRow struct {
	store   azure.Resource
	setting *azappconfig.Setting // nil when not present
	err     string               // non-404 failure (auth, network, ...)
}

type crossLoadedMsg struct {
	rows []crossRow
	err  error
}

// crossView answers "what is this key in every environment?" — it fetches
// one key+label from every discovered App Configuration store at once.
type crossView struct {
	mctx       modules.Context
	key, label string

	table   ui.Table
	spin    spinner.Model
	loading bool

	rows []crossRow

	width, height int
}

func newCrossView(mctx modules.Context, key, label string) *crossView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "STORE", Weight: 3},
		ui.Column{Title: "VALUE", Weight: 6},
		ui.Column{Title: "UPDATED", Width: 8},
	)
	t.Empty = "no App Configuration stores found"
	return &crossView{mctx: mctx, key: key, label: label, table: t, spin: sp, loading: true}
}

func (v *crossView) Init() tea.Cmd {
	mctx, key, label := v.mctx, v.key, v.label
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		opID := ui.BeginOp("comparing %s across stores", key)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()

		stores, err := azure.DiscoverResources(ctx, mctx.Cred, module{}.ResourceTypes())
		if err != nil {
			return crossLoadedMsg{err: err}
		}

		rows := make([]crossRow, len(stores))
		var wg sync.WaitGroup
		for i, store := range stores {
			wg.Add(1)
			go func(i int, store azure.Resource) {
				defer wg.Done()
				rows[i] = fetchOne(ctx, mctx, store, key, label)
			}(i, store)
		}
		wg.Wait()
		sort.Slice(rows, func(i, j int) bool { return rows[i].store.Name < rows[j].store.Name })
		return crossLoadedMsg{rows: rows}
	})
}

func fetchOne(ctx context.Context, mctx modules.Context, store azure.Resource, key, label string) crossRow {
	row := crossRow{store: store}
	endpoint, _, err := store.Endpoint()
	if err != nil {
		row.err = err.Error()
		return row
	}
	client, err := azappconfig.NewClient(endpoint, mctx.Cred, nil)
	if err != nil {
		row.err = err.Error()
		return row
	}
	opts := &azappconfig.GetSettingOptions{}
	if label != "" {
		opts.Label = &label
	}
	resp, err := client.GetSetting(ctx, key, opts)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			return row // not present — leave setting nil
		}
		row.err = err.Error()
		return row
	}
	row.setting = &resp.Setting
	return row
}

func (v *crossView) setRows() {
	rows := make([][]string, len(v.rows))
	for i, r := range v.rows {
		value, updated := "— not present —", "-"
		switch {
		case r.err != "":
			value = "error: " + r.err
		case r.setting != nil:
			value = deref(r.setting.Value)
			if r.setting.LastModified != nil {
				updated = ui.Ago(*r.setting.LastModified)
			}
		}
		rows[i] = []string{r.store.Name, value, updated}
	}
	v.table.SetRows(rows)
}

// distinctValues counts the different values present across stores.
func (v *crossView) distinctValues() int {
	seen := map[string]bool{}
	for _, r := range v.rows {
		if r.setting != nil {
			seen[deref(r.setting.Value)] = true
		}
	}
	return len(seen)
}

func (v *crossView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case crossLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.rows = msg.rows
		v.setRows()
		return v, nil

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
			case "enter":
				idx := v.table.CursorRow()
				if idx >= 0 && idx < len(v.rows) && v.rows[idx].setting != nil {
					return v, ui.Push(newValueView(v.rows[idx].store.Name, v.key, v.rows[idx].setting))
				}
				return v, nil
			case "R":
				v.loading = true
				return v, v.Init()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *crossView) View() string {
	label := ""
	if v.label != "" {
		label = " (label " + v.label + ")"
	}
	title := ui.TitleStyle.Render(" "+v.key) + ui.DimStyle.Render(label+"  across stores")
	if !v.loading {
		present := 0
		for _, r := range v.rows {
			if r.setting != nil {
				present++
			}
		}
		title += ui.DimStyle.Render(fmt.Sprintf("  ·  in %d/%d stores", present, len(v.rows)))
		if n := v.distinctValues(); n > 1 {
			title += ui.WarnStyle.Render(fmt.Sprintf("  ·  %d distinct values", n))
		}
	}
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" asking every store...")
	}
	return title + "\n" + v.table.View()
}

func (v *crossView) InputActive() bool { return v.table.InputActive() }

func (v *crossView) Breadcrumb() string { return "across stores" }

func (v *crossView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "view full value"},
		{Keys: "R", Desc: "re-fetch from all stores"},
	}
}

// --- single value viewer -------------------------------------------------------

type valueView struct {
	store, key string
	setting    *azappconfig.Setting

	vp            viewport.Model
	width, height int
}

func newValueView(store, key string, setting *azappconfig.Setting) *valueView {
	return &valueView{store: store, key: key, setting: setting}
}

func (v *valueView) Init() tea.Cmd { return nil }

func (v *valueView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-2))
		content := ui.DimStyle.Render(" store ") + ui.TitleStyle.Render(v.store) + "\n" +
			ui.DimStyle.Render(" key   ") + v.key + "\n\n" +
			prettify(deref(v.setting.Value), deref(v.setting.ContentType))
		v.vp.SetContent(content)
		return v, nil
	case tea.KeyMsg:
		switch msg.String() {
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

func (v *valueView) View() string {
	return ui.TitleStyle.Render(" "+v.store) + "\n" + v.vp.View()
}

func (v *valueView) Breadcrumb() string { return v.store }

func (v *valueView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{{Keys: "j/k", Desc: "scroll"}}
}
