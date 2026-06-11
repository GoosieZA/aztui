package app

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

// Distinct from home's messages: async results are routed to the top view,
// so shared message types across stacked views cause cross-talk.
type rvLoadedMsg struct{ resources []azure.Resource }
type rvErrMsg struct{ err error }

// resourcesView lists every discovered resource one module can open. It is
// usually seeded from the home screen's discovery so it renders instantly;
// when reached via a ":module" command it discovers on its own.
type resourcesView struct {
	mctx modules.Context
	mod  modules.Module

	table   ui.Table
	spin    spinner.Model
	loading bool

	display []azure.Resource // resources in table-row order

	width, height int
}

func newResourcesView(mctx modules.Context, mod modules.Module, seed []azure.Resource) *resourcesView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "", Width: 2},
		ui.Column{Title: "NAME", Weight: 5},
		ui.Column{Title: "RESOURCE GROUP", Weight: 4},
		ui.Column{Title: "LOCATION", Weight: 2},
		ui.Column{Title: "SUBSCRIPTION", Weight: 3},
	)
	t.Empty = "no " + mod.Title() + " resources found"
	v := &resourcesView{mctx: mctx, mod: mod, table: t, spin: sp}
	if seed != nil {
		v.setResources(seed)
	} else {
		v.loading = true
	}
	return v
}

func (v *resourcesView) Init() tea.Cmd {
	if !v.loading {
		return nil
	}
	return tea.Batch(v.spin.Tick, v.discover())
}

func (v *resourcesView) discover() tea.Cmd {
	cred, types := v.mctx.Cred, v.mod.ResourceTypes()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resources, err := azure.DiscoverResources(ctx, cred, types)
		if err != nil {
			return rvErrMsg{err}
		}
		return rvLoadedMsg{resources}
	}
}

func (v *resourcesView) setResources(resources []azure.Resource) {
	display := make([]azure.Resource, 0, len(resources))
	pinned := make(map[string]bool)
	byID := make(map[string]int, len(resources))
	for i, r := range resources {
		byID[r.ID] = i
	}
	for _, rec := range v.mctx.Config.Recents {
		if i, ok := byID[rec.Resource.ID]; ok {
			display = append(display, resources[i])
			pinned[rec.Resource.ID] = true
		}
	}
	for _, r := range resources {
		if !pinned[r.ID] {
			display = append(display, r)
		}
	}
	v.display = display

	rows := make([][]string, len(display))
	for i, r := range display {
		star := ""
		if pinned[r.ID] {
			star = "★"
		}
		sub := r.SubscriptionName
		if sub == "" {
			sub = r.SubscriptionID
		}
		rows[i] = []string{star, r.Name, r.ResourceGroup, r.Location, sub}
	}
	v.table.SetRows(rows)
}

func (v *resourcesView) open() tea.Cmd {
	idx := v.table.CursorRow()
	if idx < 0 || idx >= len(v.display) {
		return nil
	}
	res := v.display[idx]
	view, err := v.mod.Open(v.mctx, res)
	if err != nil {
		return ui.Err(err)
	}
	if err := v.mctx.Config.Touch(res); err == nil {
		v.setResources(v.display)
	}
	return ui.Push(view)
}

func (v *resourcesView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case rvLoadedMsg:
		v.loading = false
		v.setResources(msg.resources)
		return v, nil

	case rvErrMsg:
		v.loading = false
		return v, ui.Err(msg.err)

	case ui.ActivatedMsg:
		if v.loading {
			// Our discovery result may have been delivered to (and ignored
			// by) the view that was on top — start over.
			return v, tea.Batch(v.spin.Tick, v.discover())
		}
		v.setResources(v.display) // re-pin: recents may have changed below us
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
				return v, v.open()
			case "R":
				v.loading = true
				return v, tea.Batch(v.spin.Tick, v.discover())
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *resourcesView) View() string {
	title := ui.TitleStyle.Render(" "+v.mod.Icon()+" "+v.mod.Title()) +
		ui.DimStyle.Render(fmt.Sprintf("  %d resources", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" discovering resources...")
	}
	return title + "\n" + v.table.View()
}

func (v *resourcesView) InputActive() bool { return v.table.InputActive() }

func (v *resourcesView) Breadcrumb() string { return v.mod.Title() }

func (v *resourcesView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "open resource"},
		{Keys: "R", Desc: "re-discover resources"},
	}
}
