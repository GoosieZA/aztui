package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

const opTimeout = 45 * time.Second

type entry struct {
	kind     string // "container" | "queue"
	name     string
	modified string
}

type rootLoadedMsg struct {
	entries []entry
	warn    string
	err     error
}

// rootView lists a storage account's blob containers and queues.
type rootView struct {
	res    azure.Resource
	blob   *azblob.Client
	queues *azqueue.ServiceClient

	table   ui.Table
	spin    spinner.Model
	loading bool

	entries []entry

	width, height int
}

func newRootView(res azure.Resource, blob *azblob.Client, queues *azqueue.ServiceClient) *rootView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "TYPE", Width: 9},
		ui.Column{Title: "NAME", Weight: 6},
		ui.Column{Title: "MODIFIED", Width: 9},
	)
	t.Empty = "no containers or queues (or no data-plane permission)"
	return &rootView{res: res, blob: blob, queues: queues, table: t, spin: sp, loading: true}
}

func (v *rootView) Init() tea.Cmd {
	blob, queues := v.blob, v.queues
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()

		var entries []entry
		pager := blob.NewListContainersPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return rootLoadedMsg{err: fmt.Errorf("listing containers: %w", err)}
			}
			for _, c := range page.ContainerItems {
				e := entry{kind: "container", name: deref(c.Name), modified: "-"}
				if c.Properties != nil && c.Properties.LastModified != nil {
					e.modified = ui.Ago(*c.Properties.LastModified)
				}
				entries = append(entries, e)
			}
		}

		warn := ""
		if queues != nil {
			qPager := queues.NewListQueuesPager(nil)
			for qPager.More() {
				page, err := qPager.NextPage(ctx)
				if err != nil {
					warn = fmt.Sprintf("queues unavailable: %v", err)
					break
				}
				for _, q := range page.Queues {
					entries = append(entries, entry{kind: "queue", name: deref(q.Name), modified: "-"})
				}
			}
		}
		return rootLoadedMsg{entries: entries, warn: warn}
	})
}

func (v *rootView) setEntries(entries []entry) {
	v.entries = entries
	rows := make([][]string, len(entries))
	for i, e := range entries {
		rows[i] = []string{e.kind, e.name, e.modified}
	}
	v.table.SetRows(rows)
}

func (v *rootView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case rootLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setEntries(msg.entries)
		if msg.warn != "" {
			return v, ui.Warnf("%s", msg.warn)
		}
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
				if idx < 0 || idx >= len(v.entries) {
					return v, nil
				}
				e := v.entries[idx]
				if e.kind == "queue" {
					return v, ui.Push(newQueueView(v.queues, e.name))
				}
				return v, ui.Push(newBlobsView(v.blob, e.name, ""))
			case "R":
				v.loading = true
				return v, v.Init()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *rootView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d containers & queues", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" listing containers and queues...")
	}
	return title + "\n" + v.table.View()
}

func (v *rootView) InputActive() bool { return v.table.InputActive() }

func (v *rootView) Breadcrumb() string { return v.res.Name }

func (v *rootView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "browse container / peek queue"},
		{Keys: "R", Desc: "refresh"},
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
