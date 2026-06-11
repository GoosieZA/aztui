package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

const opTimeout = 45 * time.Second

// vmInfo is everything the dashboard shows about one machine.
type vmInfo struct {
	vm           *armcompute.VirtualMachine
	powerState   string
	provisioning string
	agent        string
	privateIPs   []string
	publicIPs    []string
}

func (i vmInfo) osType() string {
	if i.vm != nil && i.vm.Properties != nil && i.vm.Properties.StorageProfile != nil &&
		i.vm.Properties.StorageProfile.OSDisk != nil && i.vm.Properties.StorageProfile.OSDisk.OSType != nil {
		return string(*i.vm.Properties.StorageProfile.OSDisk.OSType)
	}
	return "unknown"
}

func (i vmInfo) size() string {
	if i.vm != nil && i.vm.Properties != nil && i.vm.Properties.HardwareProfile != nil &&
		i.vm.Properties.HardwareProfile.VMSize != nil {
		return string(*i.vm.Properties.HardwareProfile.VMSize)
	}
	return "unknown"
}

type dashLoadedMsg struct {
	info vmInfo
	err  error
}

// dashboard is the per-VM control surface.
type dashboard struct {
	mctx    modules.Context
	res     azure.Resource
	clients *clients

	spin    spinner.Model
	confirm ui.Confirm
	loading bool

	info vmInfo

	width, height int
}

func newDashboard(mctx modules.Context, res azure.Resource, c *clients) *dashboard {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	return &dashboard{mctx: mctx, res: res, clients: c, spin: sp, loading: true}
}

func (d *dashboard) Init() tea.Cmd {
	c, rg, name := d.clients, d.res.ResourceGroup, d.res.Name
	return tea.Batch(d.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		info, err := loadVM(ctx, c, rg, name)
		return dashLoadedMsg{info: info, err: err}
	})
}

func loadVM(ctx context.Context, c *clients, rg, name string) (vmInfo, error) {
	var info vmInfo
	resp, err := c.vms.Get(ctx, rg, name, &armcompute.VirtualMachinesClientGetOptions{
		Expand: to.Ptr(armcompute.InstanceViewTypesInstanceView),
	})
	if err != nil {
		return info, err
	}
	info.vm = &resp.VirtualMachine

	if p := resp.Properties; p != nil {
		if p.ProvisioningState != nil {
			info.provisioning = *p.ProvisioningState
		}
		if p.InstanceView != nil {
			for _, s := range p.InstanceView.Statuses {
				code := strFrom(s.Code)
				if strings.HasPrefix(code, "PowerState/") {
					info.powerState = strings.TrimPrefix(code, "PowerState/")
				}
			}
			if p.InstanceView.VMAgent != nil && len(p.InstanceView.VMAgent.Statuses) > 0 {
				info.agent = strFrom(p.InstanceView.VMAgent.Statuses[0].DisplayStatus)
			}
		}
		if p.NetworkProfile != nil {
			for _, ref := range p.NetworkProfile.NetworkInterfaces {
				nicRG, nicName := armParts(strFrom(ref.ID))
				nic, err := c.nics.Get(ctx, nicRG, nicName, nil)
				if err != nil {
					continue
				}
				if nic.Properties == nil {
					continue
				}
				for _, ipc := range nic.Properties.IPConfigurations {
					if ipc.Properties == nil {
						continue
					}
					if ipc.Properties.PrivateIPAddress != nil {
						info.privateIPs = append(info.privateIPs, *ipc.Properties.PrivateIPAddress)
					}
					if pub := ipc.Properties.PublicIPAddress; pub != nil && pub.ID != nil {
						pipRG, pipName := armParts(*pub.ID)
						pip, err := c.pips.Get(ctx, pipRG, pipName, nil)
						if err == nil && pip.Properties != nil && pip.Properties.IPAddress != nil {
							info.publicIPs = append(info.publicIPs, *pip.Properties.IPAddress)
						}
					}
				}
			}
		}
	}
	if info.powerState == "" {
		info.powerState = "unknown"
	}
	return info, nil
}

// powerCmd runs a lifecycle operation in the background; completion lands as
// a global toast wherever the user is.
func powerCmd(c *clients, rg, name, verb string) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("%s %s", verb, name)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		var err error
		switch verb {
		case "starting":
			p, e := c.vms.BeginStart(ctx, rg, name, nil)
			if e == nil {
				_, e = p.PollUntilDone(ctx, nil)
			}
			err = e
		case "restarting":
			p, e := c.vms.BeginRestart(ctx, rg, name, nil)
			if e == nil {
				_, e = p.PollUntilDone(ctx, nil)
			}
			err = e
		case "stopping (deallocate)":
			p, e := c.vms.BeginDeallocate(ctx, rg, name, nil)
			if e == nil {
				_, e = p.PollUntilDone(ctx, nil)
			}
			err = e
		case "powering off":
			p, e := c.vms.BeginPowerOff(ctx, rg, name, nil)
			if e == nil {
				_, e = p.PollUntilDone(ctx, nil)
			}
			err = e
		}
		if err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("%s %s failed: %v", verb, name, err), Level: ui.StatusError}
		}
		ui.RecordChange(name, verb)
		return ui.StatusMsg{Text: fmt.Sprintf("✓ %s %s done", verb, name)}
	}
}

func (d *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height = msg.Width, msg.Height
		return d, nil

	case dashLoadedMsg:
		d.loading = false
		if msg.err != nil {
			return d, ui.Err(msg.err)
		}
		d.info = msg.info
		return d, nil

	case ui.ActivatedMsg:
		d.loading = true
		return d, d.Init()

	case spinner.TickMsg:
		if !d.loading {
			return d, nil
		}
		var cmd tea.Cmd
		d.spin, cmd = d.spin.Update(msg)
		return d, cmd

	case sshDoneMsg:
		if msg.err != nil {
			return d, ui.Warnf("ssh exited: %v", msg.err)
		}
		return d, ui.Status("ssh session ended")

	case tea.KeyMsg:
		if handled, result := d.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "power" {
				verb := result.Payload.(string)
				return d, tea.Batch(
					ui.Warnf("%s %s — runs in background", verb, d.res.Name),
					powerCmd(d.clients, d.res.ResourceGroup, d.res.Name, verb),
				)
			}
			return d, nil
		}
		return d, d.handleKey(msg.String())
	}
	return d, nil
}

func (d *dashboard) handleKey(key string) tea.Cmd {
	switch key {
	case "p", "x", "X", "r":
		if cmd := ui.BlockIfReadOnly(); cmd != nil {
			return cmd
		}
	}
	switch key {
	case "p":
		return tea.Batch(
			ui.Warnf("starting %s — runs in background", d.res.Name),
			powerCmd(d.clients, d.res.ResourceGroup, d.res.Name, "starting"),
		)
	case "r":
		d.confirm.Ask("power", fmt.Sprintf("Restart %s?", d.res.Name), "restarting")
	case "x":
		d.confirm.Ask("power", fmt.Sprintf("Stop (deallocate) %s? Billing stops; IPs may change.", d.res.Name), "stopping (deallocate)")
	case "X":
		d.confirm.Ask("power", fmt.Sprintf("Power off %s without deallocating? You keep paying for it.", d.res.Name), "powering off")
	case "e":
		return ui.Push(newExtsView(d.res, d.clients, d.vmLocation()))
	case "a":
		return ui.Push(newAppsView(d.mctx, d.res, d.clients, d.info.osType()))
	case "s":
		if !strings.EqualFold(d.info.osType(), "Linux") {
			return ui.Warnf("%s is a %s VM — ssh is for Linux", d.res.Name, d.info.osType())
		}
		if len(d.info.privateIPs)+len(d.info.publicIPs) == 0 {
			return ui.Warnf("no IP addresses found for %s", d.res.Name)
		}
		return ui.Push(newSSHView(d.mctx, d.res.Name, d.info.privateIPs, d.info.publicIPs))
	case "R":
		d.loading = true
		return d.Init()
	}
	return nil
}

func (d *dashboard) vmLocation() string {
	if d.info.vm != nil && d.info.vm.Location != nil {
		return *d.info.vm.Location
	}
	return d.res.Location
}

func powerStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return ui.OKStyle.Bold(true)
	case "deallocated", "stopped":
		return ui.ErrStyle.Bold(true)
	default:
		return ui.WarnStyle.Bold(true)
	}
}

func (d *dashboard) View() string {
	title := ui.TitleStyle.Render(" " + d.res.Name)
	if d.loading {
		return title + "\n\n " + d.spin.View() + ui.DimStyle.Render(" loading instance view...")
	}
	if d.confirm.Active {
		return d.confirm.Overlay(d.width, d.height)
	}

	var b strings.Builder
	field := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(ui.DimStyle.Render(fmt.Sprintf("  %-16s", name)) + value + "\n")
	}
	b.WriteString("\n")
	field("Power", powerStyle(d.info.powerState).Render(strings.ToUpper(d.info.powerState)))
	field("OS", d.info.osType())
	field("Size", d.info.size())
	field("Provisioning", d.info.provisioning)
	field("Agent", d.info.agent)
	field("Private IPs", strings.Join(d.info.privateIPs, ", "))
	field("Public IPs", strings.Join(d.info.publicIPs, ", "))
	field("Resource group", d.res.ResourceGroup)
	field("Location", d.vmLocation())

	b.WriteString("\n")
	hint := func(k, desc string) string {
		return "  " + ui.HelpKeyStyle.Render(k) + " " + ui.HelpDescStyle.Render(desc)
	}
	b.WriteString(hint("s", "ssh") + hint("p", "start") + hint("r", "restart") + hint("x", "stop") + "\n")
	b.WriteString(hint("e", "extensions") + hint("a", "applications") + hint("R", "refresh") + "\n")

	return title + "\n" + b.String()
}

func (d *dashboard) InputActive() bool { return d.confirm.Active }

func (d *dashboard) Breadcrumb() string { return d.res.Name }

func (d *dashboard) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "s", Desc: "ssh (Linux)"},
		{Keys: "p", Desc: "start"},
		{Keys: "r", Desc: "restart"},
		{Keys: "x", Desc: "stop (deallocate)"},
		{Keys: "X", Desc: "power off (keep allocation)"},
		{Keys: "e", Desc: "extensions"},
		{Keys: "a", Desc: "applications"},
		{Keys: "R", Desc: "refresh"},
	}
}
