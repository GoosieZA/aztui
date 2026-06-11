package sql

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/ui"
)

// scaleOption is one position on the slider.
type scaleOption struct {
	SKUName  string
	Tier     string
	Family   string
	Capacity int32
}

func (o scaleOption) capacityLabel() string {
	n := strconv.FormatInt(int64(o.Capacity), 10)
	if isDTUTier(o.Tier) {
		return n + " DTU"
	}
	return n + " vCores"
}

func (o scaleOption) label() string {
	return o.SKUName + " · " + o.Tier + " · " + o.capacityLabel()
}

type scaleTier struct {
	Name string // display name
	Opts []scaleOption
}

func isDTUTier(tier string) bool {
	switch strings.ToLower(tier) {
	case "basic", "standard", "premium":
		return true
	}
	return false
}

func dtuOpts(tier string, pairs ...any) []scaleOption {
	opts := make([]scaleOption, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		opts = append(opts, scaleOption{
			SKUName:  pairs[i].(string),
			Tier:     tier,
			Capacity: int32(pairs[i+1].(int)),
		})
	}
	return opts
}

func vcoreOpts(skuName, tier string, caps ...int32) []scaleOption {
	opts := make([]scaleOption, 0, len(caps))
	for _, c := range caps {
		opts = append(opts, scaleOption{SKUName: skuName, Tier: tier, Family: "Gen5", Capacity: c})
	}
	return opts
}

var gen5Caps = []int32{2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 24, 32, 40, 80}

// The two purchasing models, as slider ladders. These are the common Gen5 /
// standard service objectives; regional availability can vary.
var models = []struct {
	Name  string
	Tiers []scaleTier
}{
	{
		Name: "DTU",
		Tiers: []scaleTier{
			{Name: "Basic", Opts: dtuOpts("Basic", "Basic", 5)},
			{Name: "Standard", Opts: dtuOpts("Standard",
				"S0", 10, "S1", 20, "S2", 50, "S3", 100, "S4", 200, "S6", 400, "S7", 800, "S9", 1600, "S12", 3000)},
			{Name: "Premium", Opts: dtuOpts("Premium",
				"P1", 125, "P2", 250, "P4", 500, "P6", 1000, "P11", 1750, "P15", 4000)},
		},
	},
	{
		Name: "vCore",
		Tiers: []scaleTier{
			{Name: "General Purpose", Opts: vcoreOpts("GP_Gen5", "GeneralPurpose", gen5Caps...)},
			{Name: "Serverless", Opts: vcoreOpts("GP_S_Gen5", "GeneralPurpose", append([]int32{1}, gen5Caps...)...)},
			{Name: "Business Critical", Opts: vcoreOpts("BC_Gen5", "BusinessCritical", gen5Caps...)},
			{Name: "Hyperscale", Opts: vcoreOpts("HS_Gen5", "Hyperscale", gen5Caps...)},
		},
	},
}

// sliderHeight is the fixed line budget of the slider block; everything
// below it belongs to the CPU chart.
const sliderHeight = 13

// scaleView is the vi-key slider — h/l moves along the size ladder, t cycles
// the tier, m switches purchasing models — with a live CPU line chart below,
// so the evidence for the resize sits right under the control.
type scaleView struct {
	client *armsql.DatabasesClient
	server azure.Resource
	db     *armsql.Database
	chart  *cpuChart

	modelIdx, tierIdx, optIdx int
	confirm                   ui.Confirm

	width, height int
}

func newScaleView(client *armsql.DatabasesClient, metrics *armmonitor.MetricsClient, server azure.Resource, db *armsql.Database) *scaleView {
	v := &scaleView{client: client, server: server, db: db, chart: newCPUChart(metrics, strFrom(db.ID))}
	v.selectCurrent()
	return v
}

// selectCurrent positions the slider on the database's current SKU, or the
// nearest thing to it.
func (v *scaleView) selectCurrent() {
	sku := effectiveSKU(v.db)
	if sku == nil {
		return
	}
	name, tier := strFrom(sku.Name), strFrom(sku.Tier)
	var capacity int32
	if sku.Capacity != nil {
		capacity = *sku.Capacity
	}

	// Exact SKU-name match first ("S2", "GP_Gen5", ...). ARM often reports
	// DTU SKU names generically ("Standard"), in which case this won't hit.
	for mi, m := range models {
		for ti, t := range m.Tiers {
			for oi, o := range t.Opts {
				exact := strings.EqualFold(o.SKUName, name) && (isDTUTier(o.Tier) || o.Capacity == capacity)
				if exact {
					v.modelIdx, v.tierIdx, v.optIdx = mi, ti, oi
					return
				}
			}
		}
	}
	// Fall back: pick the model, find the tier by name, snap to capacity.
	if !isDTUTier(tier) {
		v.modelIdx = 1
	}
	for ti, t := range v.model().Tiers {
		if len(t.Opts) > 0 && strings.EqualFold(t.Opts[0].Tier, tier) {
			v.tierIdx = ti
			break
		}
	}
	v.snapToCapacity(capacity)
}

// snapToCapacity moves optIdx to the option closest to the given capacity.
func (v *scaleView) snapToCapacity(capacity int32) {
	opts := v.tier().Opts
	best := 0
	for i, o := range opts {
		if abs32(o.Capacity-capacity) < abs32(opts[best].Capacity-capacity) {
			best = i
		}
	}
	v.optIdx = best
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

func (v *scaleView) model() struct {
	Name  string
	Tiers []scaleTier
} {
	return models[v.modelIdx]
}

func (v *scaleView) tier() scaleTier { return v.model().Tiers[v.tierIdx] }

func (v *scaleView) target() scaleOption { return v.tier().Opts[v.optIdx] }

func (v *scaleView) currentLabel() string {
	sku := effectiveSKU(v.db)
	if sku == nil {
		return "unknown"
	}
	// ARM reports DTU SKU names generically ("Standard"); the service
	// objective ("S2") is the name people actually know.
	name := strFrom(sku.Name)
	if v.db.Properties != nil && v.db.Properties.CurrentServiceObjectiveName != nil &&
		strings.EqualFold(name, strFrom(sku.Tier)) {
		name = *v.db.Properties.CurrentServiceObjectiveName
	}
	return name + " · " + strFrom(sku.Tier) + " · " + capacityLabel(sku)
}

// scaleCmd starts the scale and waits for it in the background. The result
// is a global status flash, so it lands even if the user navigated away.
func scaleCmd(client *armsql.DatabasesClient, rg, server, db string, opt scaleOption) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("scaling %s → %s", db, opt.SKUName)
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		sku := &armsql.SKU{
			Name:     to.Ptr(opt.SKUName),
			Tier:     to.Ptr(opt.Tier),
			Capacity: to.Ptr(opt.Capacity),
		}
		if opt.Family != "" {
			sku.Family = to.Ptr(opt.Family)
		}
		poller, err := client.BeginUpdate(ctx, rg, server, db, armsql.DatabaseUpdate{SKU: sku}, nil)
		if err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("scaling %s failed: %v", db, err), Level: ui.StatusError}
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return ui.StatusMsg{Text: fmt.Sprintf("scaling %s failed: %v", db, err), Level: ui.StatusError}
		}
		ui.RecordChange(server+"/"+db, "scaled to "+opt.label())
		return ui.StatusMsg{Text: fmt.Sprintf("✓ %s scaled to %s", db, opt.label())}
	}
}

func (v *scaleView) Init() tea.Cmd { return v.chart.load() }

func (v *scaleView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.chart.setSize(msg.Width-2, msg.Height-sliderHeight)
		return v, nil

	case metricsMsg, spinner.TickMsg:
		_, cmd := v.chart.update(msg)
		return v, cmd

	case tea.KeyMsg:
		if handled, result := v.confirm.Update(msg); handled {
			if result != nil && result.OK && result.Tag == "scale" {
				opt := result.Payload.(scaleOption)
				dbName := strFrom(v.db.Name)
				return v, tea.Batch(
					ui.Warnf("scaling %s → %s started (runs in background)", dbName, opt.SKUName),
					scaleCmd(v.client, v.server.ResourceGroup, v.server.Name, dbName, opt),
					ui.PopWith(refreshMsg{}),
				)
			}
			return v, nil
		}
		if handled, cmd := v.chart.update(msg); handled {
			return v, cmd
		}
		switch msg.String() {
		case "l", "right":
			if v.optIdx < len(v.tier().Opts)-1 {
				v.optIdx++
			}
		case "h", "left":
			if v.optIdx > 0 {
				v.optIdx--
			}
		case "g":
			v.optIdx = 0
		case "G":
			v.optIdx = len(v.tier().Opts) - 1
		case "t", "j", "down":
			capacity := v.target().Capacity
			v.tierIdx = (v.tierIdx + 1) % len(v.model().Tiers)
			v.snapToCapacity(capacity)
		case "T", "k", "up":
			capacity := v.target().Capacity
			v.tierIdx = (v.tierIdx + len(v.model().Tiers) - 1) % len(v.model().Tiers)
			v.snapToCapacity(capacity)
		case "m":
			v.modelIdx = (v.modelIdx + 1) % len(models)
			v.tierIdx = 0
			v.optIdx = 0
		case "enter":
			if cmd := ui.BlockIfReadOnly(); cmd != nil {
				return v, cmd
			}
			opt := v.target()
			v.confirm.Ask("scale",
				fmt.Sprintf("Scale %s from %s to %s? Connections may be dropped when the change applies.",
					strFrom(v.db.Name), v.currentLabel(), opt.label()),
				opt)
		}
		return v, nil
	}
	return v, nil
}

func (v *scaleView) slider() string {
	opts := v.tier().Opts
	const width = 44
	pos := 0
	if len(opts) > 1 {
		pos = v.optIdx * (width - 1) / (len(opts) - 1)
	}
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i == pos {
			b.WriteString(ui.TitleStyle.Render("●"))
		} else {
			b.WriteString(ui.DimStyle.Render("─"))
		}
	}
	first, last := opts[0], opts[len(opts)-1]
	return ui.DimStyle.Render(first.SKUName+" ") + "├" + b.String() + "┤" + ui.DimStyle.Render(" "+last.SKUName)
}

func (v *scaleView) View() string {
	if v.confirm.Active {
		return v.confirm.Overlay(v.width, v.height)
	}

	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }

	line("")
	line(" " + ui.TitleStyle.Render(strFrom(v.db.Name)) + ui.DimStyle.Render("  on "+v.server.Name))
	line(" " + ui.DimStyle.Render("current: ") + v.currentLabel())
	line("")

	modelNames := make([]string, len(models))
	for i, m := range models {
		if i == v.modelIdx {
			modelNames[i] = ui.SelectedRowStyle.Render(" " + m.Name + " ")
		} else {
			modelNames[i] = ui.DimStyle.Render(" " + m.Name + " ")
		}
	}
	line(" " + ui.DimStyle.Render("model ") + strings.Join(modelNames, " ") + ui.DimStyle.Render("   (m)"))

	tierNames := make([]string, len(v.model().Tiers))
	for i, t := range v.model().Tiers {
		if i == v.tierIdx {
			tierNames[i] = ui.SelectedRowStyle.Render(" " + t.Name + " ")
		} else {
			tierNames[i] = ui.DimStyle.Render(" " + t.Name + " ")
		}
	}
	line(" " + ui.DimStyle.Render("tier  ") + strings.Join(tierNames, " ") + ui.DimStyle.Render("   (t)"))
	line("")
	line("   " + v.slider())
	line("")

	target := v.target()
	arrow := "   "
	if target.label() != v.currentLabel() {
		arrow = " → "
	}
	line(ui.DimStyle.Render(" target") + arrow + ui.TitleStyle.Render(target.label()))
	line("")
	line(ui.DimStyle.Render(" h/l size · t tier · m model · enter apply · esc cancel"))

	slider := lipgloss.NewStyle().MarginLeft(max(0, (v.width-70)/2)).Render(b.String())
	if chart := v.chart.view(); chart != "" {
		return slider + "\n" + chart
	}
	return slider
}

func (v *scaleView) InputActive() bool { return v.confirm.Active }

func (v *scaleView) Breadcrumb() string { return "scale " + strFrom(v.db.Name) }

func (v *scaleView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "h/l", Desc: "smaller / larger"},
		{Keys: "g/G", Desc: "smallest / largest"},
		{Keys: "t/T", Desc: "next / previous tier"},
		{Keys: "m", Desc: "DTU ↔ vCore model"},
		{Keys: "1/2/3", Desc: "graph range 1h/24h/7d"},
		{Keys: "R", Desc: "refresh graph"},
		{Keys: "enter", Desc: "apply (with confirm)"},
	}
}
