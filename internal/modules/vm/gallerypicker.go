package vm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

// galleryAppRow is one installable application found in a compute gallery.
type galleryAppRow struct {
	subscription string
	rg           string
	gallery      string
	name         string
	osType       string
	description  string
}

type galleryAppsMsg struct {
	apps []galleryAppRow
	err  error
}

type galleryVersionRow struct {
	name      string
	id        string
	published string
	excluded  bool // ExcludeFromLatest
}

type galleryVersionsMsg struct {
	versions []galleryVersionRow
	err      error
}

const (
	modeApps = iota
	modeVersions
)

// galleryPickerView browses every compute gallery the credential can see and
// installs a selected application version on the VM — no resource IDs needed.
type galleryPickerView struct {
	mctx        modules.Context
	vmRes       azure.Resource
	clients     *clients
	osType      string
	currentApps []*armcompute.VMGalleryApplication

	mode     int
	apps     []galleryAppRow
	selApp   galleryAppRow
	versions []galleryVersionRow

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	width, height int
}

func newGalleryPicker(mctx modules.Context, vmRes azure.Resource, c *clients, osType string, current []*armcompute.VMGalleryApplication) *galleryPickerView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	v := &galleryPickerView{mctx: mctx, vmRes: vmRes, clients: c, osType: osType, currentApps: current, loading: true}
	v.useAppColumns()
	return v
}

func (v *galleryPickerView) useAppColumns() {
	v.table = ui.NewTable(
		ui.Column{Title: "APPLICATION", Weight: 3},
		ui.Column{Title: "GALLERY", Weight: 2},
		ui.Column{Title: "OS", Width: 8},
		ui.Column{Title: "DESCRIPTION", Weight: 5},
	)
	v.table.Empty = "no gallery applications found for this OS"
	v.table.SetSize(v.width, max(1, v.height-2))
}

func (v *galleryPickerView) useVersionColumns() {
	v.table = ui.NewTable(
		ui.Column{Title: "VERSION", Weight: 2},
		ui.Column{Title: "PUBLISHED", Width: 12},
		ui.Column{Title: "IN LATEST", Width: 9},
	)
	v.table.Empty = "no versions published"
	v.table.SetSize(v.width, max(1, v.height-2))
}

func (v *galleryPickerView) Init() tea.Cmd {
	mctx, osType := v.mctx, v.osType
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		opID := ui.BeginOp("browsing gallery applications")
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		galleries, err := azure.DiscoverResources(ctx, mctx.Cred, []string{"microsoft.compute/galleries"})
		if err != nil {
			return galleryAppsMsg{err: err}
		}

		var apps []galleryAppRow
		clientCache := map[string]*armcompute.GalleryApplicationsClient{}
		for _, g := range galleries {
			client, ok := clientCache[g.SubscriptionID]
			if !ok {
				client, err = armcompute.NewGalleryApplicationsClient(g.SubscriptionID, mctx.Cred, nil)
				if err != nil {
					continue
				}
				clientCache[g.SubscriptionID] = client
			}
			pager := client.NewListByGalleryPager(g.ResourceGroup, g.Name, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					break // a single inaccessible gallery shouldn't kill the browse
				}
				for _, app := range page.Value {
					row := galleryAppRow{
						subscription: g.SubscriptionID,
						rg:           g.ResourceGroup,
						gallery:      g.Name,
						name:         strFrom(app.Name),
					}
					if app.Properties != nil {
						if app.Properties.SupportedOSType != nil {
							row.osType = string(*app.Properties.SupportedOSType)
						}
						row.description = strFrom(app.Properties.Description)
					}
					// Only offer applications that can run on this VM.
					if row.osType != "" && osType != "" && !strings.EqualFold(row.osType, osType) {
						continue
					}
					apps = append(apps, row)
				}
			}
		}
		sort.Slice(apps, func(i, j int) bool { return apps[i].name < apps[j].name })
		return galleryAppsMsg{apps: apps}
	})
}

func (v *galleryPickerView) loadVersions(app galleryAppRow) tea.Cmd {
	cred := v.mctx.Cred
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		client, err := armcompute.NewGalleryApplicationVersionsClient(app.subscription, cred, nil)
		if err != nil {
			return galleryVersionsMsg{err: err}
		}
		var versions []galleryVersionRow
		pager := client.NewListByGalleryApplicationPager(app.rg, app.gallery, app.name, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return galleryVersionsMsg{err: err}
			}
			for _, ver := range page.Value {
				row := galleryVersionRow{name: strFrom(ver.Name), id: strFrom(ver.ID)}
				if p := ver.Properties; p != nil && p.PublishingProfile != nil {
					if p.PublishingProfile.PublishedDate != nil {
						row.published = p.PublishingProfile.PublishedDate.Format("2006-01-02")
					}
					if p.PublishingProfile.ExcludeFromLatest != nil {
						row.excluded = *p.PublishingProfile.ExcludeFromLatest
					}
				}
				versions = append(versions, row)
			}
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].name > versions[j].name })
		return galleryVersionsMsg{versions: versions}
	}
}

func (v *galleryPickerView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case galleryAppsMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.apps = msg.apps
		rows := make([][]string, len(msg.apps))
		for i, a := range msg.apps {
			rows[i] = []string{a.name, a.gallery, a.osType, a.description}
		}
		v.table.SetRows(rows)
		return v, nil

	case galleryVersionsMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.versions = msg.versions
		rows := make([][]string, len(msg.versions))
		for i, ver := range msg.versions {
			latest := "✓"
			if ver.excluded {
				latest = ""
			}
			rows[i] = []string{ver.name, ver.published, latest}
		}
		v.table.SetRows(rows)
		return v, nil

	case ui.ActivatedMsg:
		if v.loading && v.mode == modeApps {
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
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "install" {
				ver := result.Payload.(galleryVersionRow)
				next := append(append([]*armcompute.VMGalleryApplication{}, v.currentApps...), &armcompute.VMGalleryApplication{
					PackageReferenceID: to.Ptr(ver.id),
				})
				action := fmt.Sprintf("installed application %s %s", v.selApp.name, ver.name)
				return v, tea.Batch(
					ui.Warnf("installing %s %s — runs in background", v.selApp.name, ver.name),
					applyAppsCmd(v.clients, v.vmRes.ResourceGroup, v.vmRes.Name, action, next),
					ui.Pop(),
				)
			}
			return v, nil
		}
		if !v.table.InputActive() {
			switch msg.String() {
			case "enter":
				return v, v.choose()
			case "h", "backspace":
				if v.mode == modeVersions {
					v.mode = modeApps
					v.useAppColumns()
					rows := make([][]string, len(v.apps))
					for i, a := range v.apps {
						rows[i] = []string{a.name, a.gallery, a.osType, a.description}
					}
					v.table.SetRows(rows)
					return v, nil
				}
			case "R":
				v.loading = true
				if v.mode == modeVersions {
					return v, tea.Batch(v.spin.Tick, v.loadVersions(v.selApp))
				}
				return v, tea.Batch(v.spin.Tick, v.Init())
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *galleryPickerView) choose() tea.Cmd {
	idx := v.table.CursorRow()
	if v.mode == modeApps {
		if idx < 0 || idx >= len(v.apps) {
			return nil
		}
		v.selApp = v.apps[idx]
		v.mode = modeVersions
		v.loading = true
		v.useVersionColumns()
		return tea.Batch(v.spin.Tick, v.loadVersions(v.selApp))
	}
	if idx < 0 || idx >= len(v.versions) {
		return nil
	}
	if cmd := ui.BlockIfReadOnly(); cmd != nil {
		return cmd
	}
	ver := v.versions[idx]
	for _, cur := range v.currentApps {
		if strings.EqualFold(strFrom(cur.PackageReferenceID), ver.id) {
			return ui.Warnf("%s %s is already installed", v.selApp.name, ver.name)
		}
	}
	v.confirm.Ask("install", fmt.Sprintf("Install %s %s on %s?", v.selApp.name, ver.name, v.vmRes.Name), ver)
	return nil
}

func (v *galleryPickerView) View() string {
	var title string
	if v.mode == modeApps {
		title = ui.TitleStyle.Render(" available applications") +
			ui.DimStyle.Render(fmt.Sprintf("  for %s (%s)  ·  %d found", v.vmRes.Name, v.osType, v.table.Count()))
	} else {
		title = ui.TitleStyle.Render(" "+v.selApp.name) +
			ui.DimStyle.Render("  versions  ·  h to go back")
	}
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" searching galleries...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *galleryPickerView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *galleryPickerView) Breadcrumb() string {
	if v.mode == modeVersions {
		return v.selApp.name
	}
	return "install app"
}

func (v *galleryPickerView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "choose application / install version"},
		{Keys: "h", Desc: "back to applications"},
		{Keys: "/", Desc: "filter"},
		{Keys: "R", Desc: "refresh"},
	}
}
