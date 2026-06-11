package vm

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

type appsLoadedMsg struct {
	apps []*armcompute.VMGalleryApplication
	err  error
}

// appPackage extracts "application" and "version" from a gallery
// application version resource ID.
func appPackage(id string) (name, version string) {
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i++ {
		switch strings.ToLower(parts[i]) {
		case "applications":
			name = parts[i+1]
		case "versions":
			version = parts[i+1]
		}
	}
	if name == "" {
		name = id
	}
	return name, version
}

// appsView manages the VM's gallery applications (applicationProfile).
type appsView struct {
	mctx    modules.Context
	res     azure.Resource
	clients *clients
	osType  string

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	apps []*armcompute.VMGalleryApplication

	width, height int
}

func newAppsView(mctx modules.Context, res azure.Resource, c *clients, osType string) *appsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "APPLICATION", Weight: 4},
		ui.Column{Title: "VERSION", Width: 10},
		ui.Column{Title: "ORDER", Width: 5},
		ui.Column{Title: "FAIL-FATAL", Width: 10},
		ui.Column{Title: "PACKAGE ID", Weight: 6},
	)
	t.Empty = "no VM applications on this machine"
	return &appsView{mctx: mctx, res: res, clients: c, osType: osType, table: t, spin: sp, loading: true}
}

func (v *appsView) Init() tea.Cmd {
	c, rg, name := v.clients, v.res.ResourceGroup, v.res.Name
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		resp, err := c.vms.Get(ctx, rg, name, nil)
		if err != nil {
			return appsLoadedMsg{err: err}
		}
		var apps []*armcompute.VMGalleryApplication
		if resp.Properties != nil && resp.Properties.ApplicationProfile != nil {
			apps = resp.Properties.ApplicationProfile.GalleryApplications
		}
		return appsLoadedMsg{apps: apps}
	})
}

func (v *appsView) setApps(apps []*armcompute.VMGalleryApplication) {
	v.apps = apps
	rows := make([][]string, len(apps))
	for i, a := range apps {
		name, version := appPackage(strFrom(a.PackageReferenceID))
		order := "0"
		if a.Order != nil {
			order = strconv.FormatInt(int64(*a.Order), 10)
		}
		fatal := ""
		if a.TreatFailureAsDeploymentFailure != nil && *a.TreatFailureAsDeploymentFailure {
			fatal = "✓"
		}
		rows[i] = []string{name, version, order, fatal, strFrom(a.PackageReferenceID)}
	}
	v.table.SetRows(rows)
}

// applyAppsCmd replaces the VM's gallery application list in the background.
func applyAppsCmd(c *clients, rg, vmName, action string, apps []*armcompute.VMGalleryApplication) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("%s on %s", action, vmName)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		update := armcompute.VirtualMachineUpdate{
			Properties: &armcompute.VirtualMachineProperties{
				ApplicationProfile: &armcompute.ApplicationProfile{GalleryApplications: apps},
			},
		}
		p, err := c.vms.BeginUpdate(ctx, rg, vmName, update, nil)
		if err == nil {
			_, err = p.PollUntilDone(ctx, nil)
		}
		if err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("%s failed: %v", action, err), Level: ui.StatusError}
		}
		ui.RecordChange(vmName, action)
		return ui.StatusMsg{Text: fmt.Sprintf("✓ %s on %s", action, vmName)}
	}
}

func (v *appsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case appsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setApps(msg.apps)
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
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "remove" {
				idx := result.Payload.(int)
				name, _ := appPackage(strFrom(v.apps[idx].PackageReferenceID))
				next := append(append([]*armcompute.VMGalleryApplication{}, v.apps[:idx]...), v.apps[idx+1:]...)
				return v, tea.Batch(
					ui.Warnf("removing application %s — runs in background", name),
					applyAppsCmd(v.clients, v.res.ResourceGroup, v.res.Name, "removed application "+name, next),
				)
			}
			return v, nil
		}
		if !v.table.InputActive() {
			switch msg.String() {
			case "n":
				if cmd := ui.BlockIfReadOnly(); cmd != nil {
					return v, cmd
				}
				return v, ui.Push(newGalleryPicker(v.mctx, v.res, v.clients, v.osType, v.apps))
			case "d":
				if cmd := ui.BlockIfReadOnly(); cmd != nil {
					return v, cmd
				}
				idx := v.table.CursorRow()
				if idx >= 0 && idx < len(v.apps) {
					name, _ := appPackage(strFrom(v.apps[idx].PackageReferenceID))
					v.confirm.Ask("remove", fmt.Sprintf("Remove application %q from %s?", name, v.res.Name), idx)
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

func (v *appsView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d applications", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" loading application profile...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *appsView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *appsView) Breadcrumb() string { return "applications" }

func (v *appsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "n", Desc: "browse & install applications"},
		{Keys: "d", Desc: "remove application"},
		{Keys: "R", Desc: "refresh"},
	}
}
