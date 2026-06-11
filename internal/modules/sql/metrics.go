package sql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/GoosieZA/aztui/internal/ui"
)

type metricPoint struct {
	t   time.Time
	avg float64
	max float64
}

type metricsMsg struct {
	points []metricPoint
	err    error
}

type metricRange struct {
	label     string
	interval  string
	span      time.Duration
	timeLabel string // x-axis time format
}

var metricRanges = []metricRange{
	{"1h", "PT1M", time.Hour, "15:04"},
	{"24h", "PT15M", 24 * time.Hour, "15:04"},
	{"7d", "PT1H", 7 * 24 * time.Hour, "Jan 2"},
}

var (
	avgLine = lipgloss.NewStyle().Foreground(ui.ColorAccent)
	maxLine = lipgloss.NewStyle().Foreground(ui.ColorDim)
)

// cpuChart is an embeddable Azure Monitor cpu_percent line chart (average +
// maximum), drawn with braille cells. The scale view shows it under the
// slider so you can see whether a resize is justified before applying it.
type cpuChart struct {
	client     *armmonitor.MetricsClient
	resourceID string

	rangeIdx int
	points   []metricPoint
	loading  bool
	fetched  bool
	spin     spinner.Model

	cache         string // rendered chart — rebuilt on data/size changes only
	width, height int    // full block budget (lines), set by the parent
}

func newCPUChart(client *armmonitor.MetricsClient, resourceID string) *cpuChart {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	return &cpuChart{client: client, resourceID: resourceID, rangeIdx: 1, loading: true}
}

func (c *cpuChart) setSize(w, h int) {
	c.width, c.height = w, h
	c.rebuild()
}

func (c *cpuChart) load() tea.Cmd {
	client, id := c.client, c.resourceID
	r := metricRanges[c.rangeIdx]
	return tea.Batch(c.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		now := time.Now().UTC().Truncate(time.Minute)
		timespan := now.Add(-r.span).Format(time.RFC3339) + "/" + now.Format(time.RFC3339)
		resp, err := client.List(ctx, id, &armmonitor.MetricsClientListOptions{
			Timespan:    to.Ptr(timespan),
			Interval:    to.Ptr(r.interval),
			Metricnames: to.Ptr("cpu_percent"),
			Aggregation: to.Ptr("Average,Maximum"),
		})
		if err != nil {
			return metricsMsg{err: err}
		}
		var points []metricPoint
		for _, metric := range resp.Value {
			for _, series := range metric.Timeseries {
				for _, d := range series.Data {
					if d.TimeStamp == nil || (d.Average == nil && d.Maximum == nil) {
						continue
					}
					p := metricPoint{t: *d.TimeStamp}
					if d.Average != nil {
						p.avg = *d.Average
					}
					if d.Maximum != nil {
						p.max = *d.Maximum
					}
					if p.max < p.avg {
						p.max = p.avg
					}
					points = append(points, p)
				}
			}
		}
		return metricsMsg{points: points}
	})
}

// update handles the chart's messages and keys; handled reports whether the
// message was consumed.
func (c *cpuChart) update(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case metricsMsg:
		c.loading = false
		c.fetched = true
		if msg.err != nil {
			c.points = nil
			c.rebuild()
			return true, ui.Err(msg.err)
		}
		c.points = msg.points
		c.rebuild()
		return true, nil

	case spinner.TickMsg:
		if !c.loading {
			return false, nil
		}
		var cmd tea.Cmd
		c.spin, cmd = c.spin.Update(msg)
		return true, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "1", "2", "3":
			idx := int(msg.String()[0] - '1')
			if idx != c.rangeIdx {
				c.rangeIdx = idx
				c.loading = true
				return true, c.load()
			}
			return true, nil
		case "R":
			c.loading = true
			return true, c.load()
		}
	}
	return false, nil
}

func (c *cpuChart) header() string {
	var ranges []string
	for i, r := range metricRanges {
		if i == c.rangeIdx {
			ranges = append(ranges, ui.SelectedRowStyle.Render(" "+r.label+" "))
		} else {
			ranges = append(ranges, ui.DimStyle.Render(" "+r.label+" "))
		}
	}
	return ui.DimStyle.Render(" cpu   ") + strings.Join(ranges, " ") + ui.DimStyle.Render("   (1/2/3)")
}

func (c *cpuChart) view() string {
	if c.height < 7 {
		return ""
	}
	if c.loading && !c.fetched {
		return c.header() + "\n\n " + c.spin.View() + ui.DimStyle.Render(" fetching cpu_percent...")
	}
	out := c.header()
	if c.loading {
		out += ui.DimStyle.Render("  " + c.spin.View())
	}
	return out + "\n" + c.cache
}

func (c *cpuChart) rebuild() {
	if len(c.points) == 0 {
		if c.fetched {
			c.cache = "\n " + ui.DimStyle.Render("no metric data — the database may be idle or paused")
		}
		return
	}
	// Budget: header is drawn separately; axis, x labels, and legend take 3.
	chartRows := max(4, c.height-4)
	gutter := 7
	chartCols := max(10, c.width-gutter-2)
	c.cache = renderLineChart(c.points, chartCols, chartRows, metricRanges[c.rangeIdx].timeLabel)
}

// --- braille line rendering ----------------------------------------------------

// brailleBit returns the braille dot bit for a pixel within a 2x4 cell.
var brailleBits = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

type brailleGrid struct {
	w, h int // cells
	avg  [][]rune
	max  [][]rune
}

func newBrailleGrid(w, h int) *brailleGrid {
	g := &brailleGrid{w: w, h: h, avg: make([][]rune, h), max: make([][]rune, h)}
	for r := range g.avg {
		g.avg[r] = make([]rune, w)
		g.max[r] = make([]rune, w)
	}
	return g
}

func (g *brailleGrid) plot(series [][]rune, x, y int) {
	if x < 0 || y < 0 || x >= g.w*2 || y >= g.h*4 {
		return
	}
	series[y/4][x/2] |= brailleBits[y%4][x%2]
}

// lineTo draws a pixel line between two points (Bresenham).
func (g *brailleGrid) lineTo(series [][]rune, x0, y0, x1, y1 int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		g.plot(series, x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		if e2 := 2 * err; e2 >= dy {
			err += dy
			x0 += sx
		} else {
			err += dx
			y0 += sy
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// renderLineChart draws avg (accent) and max (dim) as braille polylines with
// a y-axis gutter, x-axis time labels, and a legend/stats line.
func renderLineChart(points []metricPoint, cols, rows int, timeFmt string) string {
	peak := 0.0
	for _, p := range points {
		if p.max > peak {
			peak = p.max
		}
	}
	yMax := niceCeil(peak)

	g := newBrailleGrid(cols, rows)
	pxW, pxH := cols*2, rows*4
	toX := func(i int) int {
		if len(points) == 1 {
			return 0
		}
		return i * (pxW - 1) / (len(points) - 1)
	}
	toY := func(val float64) int {
		y := pxH - 1 - int(val/yMax*float64(pxH-1)+0.5)
		return min(max(y, 0), pxH-1)
	}

	for i := 1; i < len(points); i++ {
		g.lineTo(g.max, toX(i-1), toY(points[i-1].max), toX(i), toY(points[i].max))
	}
	for i := 1; i < len(points); i++ {
		g.lineTo(g.avg, toX(i-1), toY(points[i-1].avg), toX(i), toY(points[i].avg))
	}
	if len(points) == 1 {
		g.plot(g.max, 0, toY(points[0].max))
		g.plot(g.avg, 0, toY(points[0].avg))
	}

	var b strings.Builder
	for r := 0; r < rows; r++ {
		label := "      "
		switch r {
		case 0:
			label = fmt.Sprintf("%5.0f%%", yMax)
		case rows / 2:
			label = fmt.Sprintf("%5.0f%%", yMax/2)
		}
		b.WriteString(ui.DimStyle.Render(label) + ui.DimStyle.Render("┤"))
		for col := 0; col < cols; col++ {
			a, m := g.avg[r][col], g.max[r][col]
			switch {
			case a != 0:
				b.WriteString(avgLine.Render(string(rune(0x2800) | a | m)))
			case m != 0:
				b.WriteString(maxLine.Render(string(rune(0x2800) | m)))
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(ui.DimStyle.Render("    0%└"+strings.Repeat("─", cols)) + "\n")

	from := points[0].t.Local().Format(timeFmt)
	until := points[len(points)-1].t.Local().Format(timeFmt)
	axis := "       " + from + strings.Repeat(" ", max(1, cols-runewidth.StringWidth(from)-runewidth.StringWidth(until))) + until
	b.WriteString(ui.DimStyle.Render(axis) + "\n")

	overallAvg, overallPeak := 0.0, 0.0
	for _, p := range points {
		overallAvg += p.avg
		if p.max > overallPeak {
			overallPeak = p.max
		}
	}
	overallAvg /= float64(len(points))
	b.WriteString("       " + avgLine.Render("⠉⠉") + ui.HelpDescStyle.Render(" avg  ") +
		maxLine.Render("⠉⠉") + ui.HelpDescStyle.Render(" max") +
		ui.DimStyle.Render(fmt.Sprintf("   ·   avg %.1f%%  ·  peak %.1f%%", overallAvg, overallPeak)))
	return b.String()
}

// niceCeil picks a readable y-axis maximum for a percentage chart.
func niceCeil(peak float64) float64 {
	for _, c := range []float64{5, 10, 25, 50, 75, 100} {
		if peak <= c {
			return c
		}
	}
	return 100
}
