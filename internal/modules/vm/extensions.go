package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

type extsLoadedMsg struct {
	exts []*armcompute.VirtualMachineExtension
	err  error
}

type extSpec struct {
	Name                    string         `yaml:"name"`
	Publisher               string         `yaml:"publisher"`
	Type                    string         `yaml:"type"`
	TypeHandlerVersion      string         `yaml:"type_handler_version"`
	AutoUpgradeMinorVersion bool           `yaml:"auto_upgrade_minor_version"`
	Settings                map[string]any `yaml:"settings"`
	ProtectedSettings       map[string]any `yaml:"protected_settings"`
}

const extTemplate = `# aztui — install a VM extension.
# Save and quit to install; quit without saving changes to cancel.
# Example: publisher Microsoft.Azure.Extensions, type CustomScript,
# type_handler_version "2.1".
name: ""
publisher: ""
type: ""
type_handler_version: ""
auto_upgrade_minor_version: true
settings: {}
protected_settings: {}
`

// extsView lists and manages one VM's extensions.
type extsView struct {
	res      azure.Resource
	clients  *clients
	location string

	table   ui.Table
	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	exts []*armcompute.VirtualMachineExtension

	width, height int
}

func newExtsView(res azure.Resource, c *clients, location string) *extsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "NAME", Weight: 4},
		ui.Column{Title: "PUBLISHER", Weight: 4},
		ui.Column{Title: "TYPE", Weight: 3},
		ui.Column{Title: "VERSION", Width: 8},
		ui.Column{Title: "AUTO-UP", Width: 7},
		ui.Column{Title: "STATE", Width: 11},
	)
	t.Empty = "no extensions installed"
	return &extsView{res: res, clients: c, location: location, table: t, spin: sp, loading: true}
}

func (v *extsView) Init() tea.Cmd {
	c, rg, name := v.clients, v.res.ResourceGroup, v.res.Name
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		resp, err := c.exts.List(ctx, rg, name, nil)
		if err != nil {
			return extsLoadedMsg{err: err}
		}
		return extsLoadedMsg{exts: resp.Value}
	})
}

func (v *extsView) setExts(exts []*armcompute.VirtualMachineExtension) {
	v.exts = exts
	rows := make([][]string, len(exts))
	for i, e := range exts {
		pub, typ, ver, auto, state := "", "", "", "-", ""
		if p := e.Properties; p != nil {
			pub, typ, ver = strFrom(p.Publisher), strFrom(p.Type), strFrom(p.TypeHandlerVersion)
			if p.AutoUpgradeMinorVersion != nil && *p.AutoUpgradeMinorVersion {
				auto = "✓"
			}
			state = strFrom(p.ProvisioningState)
		}
		rows[i] = []string{strFrom(e.Name), pub, typ, ver, auto, state}
	}
	v.table.SetRows(rows)
}

// installCmd runs in the background — extension provisioning takes minutes.
func installCmd(c *clients, rg, vmName, location string, spec extSpec) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("installing extension %s", spec.Name)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		ext := armcompute.VirtualMachineExtension{
			Location: to.Ptr(location),
			Properties: &armcompute.VirtualMachineExtensionProperties{
				Publisher:               to.Ptr(spec.Publisher),
				Type:                    to.Ptr(spec.Type),
				TypeHandlerVersion:      to.Ptr(spec.TypeHandlerVersion),
				AutoUpgradeMinorVersion: to.Ptr(spec.AutoUpgradeMinorVersion),
			},
		}
		if len(spec.Settings) > 0 {
			ext.Properties.Settings = spec.Settings
		}
		if len(spec.ProtectedSettings) > 0 {
			ext.Properties.ProtectedSettings = spec.ProtectedSettings
		}
		p, err := c.exts.BeginCreateOrUpdate(ctx, rg, vmName, spec.Name, ext, nil)
		if err == nil {
			_, err = p.PollUntilDone(ctx, nil)
		}
		if err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("installing %s failed: %v", spec.Name, err), Level: ui.StatusError}
		}
		ui.RecordChange(vmName, "installed extension "+spec.Name)
		return ui.StatusMsg{Text: fmt.Sprintf("✓ extension %s installed on %s", spec.Name, vmName)}
	}
}

func uninstallCmd(c *clients, rg, vmName, extName string) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("removing extension %s", extName)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		p, err := c.exts.BeginDelete(ctx, rg, vmName, extName, nil)
		if err == nil {
			_, err = p.PollUntilDone(ctx, nil)
		}
		if err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("removing %s failed: %v", extName, err), Level: ui.StatusError}
		}
		ui.RecordChange(vmName, "removed extension "+extName)
		return ui.StatusMsg{Text: fmt.Sprintf("✓ extension %s removed from %s", extName, vmName)}
	}
}

func (v *extsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case extsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.setExts(msg.exts)
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

	case ui.EditorResult:
		if msg.Err != nil {
			return v, ui.Errorf("editor: %v", msg.Err)
		}
		if msg.Canceled {
			return v, ui.Status("no changes")
		}
		var spec extSpec
		if err := yaml.Unmarshal(msg.Content, &spec); err != nil {
			return v, ui.Errorf("invalid yaml: %v", err)
		}
		if spec.Name == "" || spec.Publisher == "" || spec.Type == "" || spec.TypeHandlerVersion == "" {
			return v, ui.Errorf("name, publisher, type, and type_handler_version are all required")
		}
		return v, tea.Batch(
			ui.Warnf("installing %s — runs in background", spec.Name),
			installCmd(v.clients, v.res.ResourceGroup, v.res.Name, v.location, spec),
		)

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "uninstall" {
				extName := result.Payload.(string)
				return v, tea.Batch(
					ui.Warnf("removing %s — runs in background", extName),
					uninstallCmd(v.clients, v.res.ResourceGroup, v.res.Name, extName),
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
				return v, ui.OpenEditor("ext-install", []byte(extTemplate), "yaml")
			case "d":
				if cmd := ui.BlockIfReadOnly(); cmd != nil {
					return v, cmd
				}
				idx := v.table.CursorRow()
				if idx >= 0 && idx < len(v.exts) {
					name := strFrom(v.exts[idx].Name)
					v.confirm.Ask("uninstall", fmt.Sprintf("Uninstall extension %q from %s?", name, v.res.Name), name)
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

func (v *extsView) View() string {
	title := ui.TitleStyle.Render(" "+v.res.Name) +
		ui.DimStyle.Render(fmt.Sprintf("  %d extensions", v.table.Count()))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" listing extensions...")
	}
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}
	return title + "\n" + v.table.View()
}

func (v *extsView) InputActive() bool { return v.table.InputActive() || v.confirm.Active }

func (v *extsView) Breadcrumb() string { return "extensions" }

func (v *extsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "n", Desc: "install extension"},
		{Keys: "d", Desc: "uninstall extension"},
		{Keys: "R", Desc: "refresh"},
	}
}
