package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

const (
	tileHeight  = 5 // 3 content lines + borders
	homeRecents = 6
)

// Focus zones. Must live in their own const block: an iota continuing from
// other constants once silently broke initial keyboard focus.
const (
	zoneTiles = iota
	zoneRecents
)

type resourcesLoadedMsg struct{ resources []azure.Resource }
type discoverErrMsg struct{ err error }

// Home is the launcher: one tile per module (icon, name, resource count)
// plus the most recently opened resources. Tiles are keyboard- and
// mouse-driven; selecting one opens that module's resource list.
type Home struct {
	mctx modules.Context

	spin    spinner.Model
	loading bool
	err     error

	resources []azure.Resource
	recents   []azure.Resource

	zone    int
	tileIdx int
	recIdx  int

	lay           homeLayout // geometry of the last render, for mouse hit-testing
	width, height int
}

func NewHome(mctx modules.Context) *Home {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	h := &Home{mctx: mctx, spin: sp, loading: true, zone: zoneTiles}
	// Show recents straight from config while discovery runs.
	for _, rec := range mctx.Config.Recents {
		if len(h.recents) < homeRecents {
			h.recents = append(h.recents, rec.Resource)
		}
	}
	return h
}

func (h *Home) Init() tea.Cmd {
	return tea.Batch(h.spin.Tick, h.discover())
}

func (h *Home) discover() tea.Cmd {
	cred := h.mctx.Cred
	types := modules.AllTypes()
	return func() tea.Msg {
		opID := ui.BeginOp("discovering resources")
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resources, err := azure.DiscoverResources(ctx, cred, types)
		if err != nil {
			return discoverErrMsg{err}
		}
		return resourcesLoadedMsg{resources}
	}
}

// resolveRecents maps config recents onto discovered resources, dropping any
// that no longer exist (and picking up their endpoint properties).
func (h *Home) resolveRecents() {
	byID := make(map[string]int, len(h.resources))
	for i, r := range h.resources {
		byID[r.ID] = i
	}
	h.recents = h.recents[:0]
	for _, rec := range h.mctx.Config.Recents {
		if i, ok := byID[rec.Resource.ID]; ok && len(h.recents) < homeRecents {
			h.recents = append(h.recents, h.resources[i])
		}
	}
	if h.recIdx >= len(h.recents) {
		h.recIdx = max(0, len(h.recents)-1)
	}
	if len(h.recents) == 0 {
		h.zone = zoneTiles
	}
}

func (h *Home) countFor(mod modules.Module) int {
	types := make(map[string]bool)
	for _, t := range mod.ResourceTypes() {
		types[t] = true
	}
	n := 0
	for _, r := range h.resources {
		if types[r.Type] {
			n++
		}
	}
	return n
}

// seedFor returns the discovered resources a module can open, or nil while
// discovery is still running (the resources view then discovers on its own).
func (h *Home) seedFor(mod modules.Module) []azure.Resource {
	if h.loading || h.err != nil {
		return nil
	}
	types := make(map[string]bool)
	for _, t := range mod.ResourceTypes() {
		types[t] = true
	}
	seed := make([]azure.Resource, 0, 8)
	for _, r := range h.resources {
		if types[r.Type] {
			seed = append(seed, r)
		}
	}
	return seed
}

func (h *Home) openTile() tea.Cmd {
	mods := modules.All()
	if h.tileIdx < 0 || h.tileIdx >= len(mods) {
		return nil
	}
	mod := mods[h.tileIdx]
	return ui.Push(newResourcesView(h.mctx, mod, h.seedFor(mod)))
}

func (h *Home) openRecent(i int) tea.Cmd {
	if i < 0 || i >= len(h.recents) {
		return nil
	}
	if h.loading {
		return ui.Warnf("still discovering — one moment")
	}
	res := h.recents[i]
	mod := modules.ForType(res.Type)
	if mod == nil {
		return ui.Errorf("no module registered for %s", res.Type)
	}
	view, err := mod.Open(h.mctx, res)
	if err != nil {
		return ui.Err(err)
	}
	if err := h.mctx.Config.Touch(res); err == nil {
		h.resolveRecents()
	}
	return ui.Push(view)
}

// --- layout ----------------------------------------------------------------

// homeLayout records where the last render put things, so mouse clicks can
// be mapped back to tiles and recent rows.
type homeLayout struct {
	offsetX  int // outer left margin applied to every line
	tilesY   int
	tileW    int
	perRow   int
	tileRows int
	recentsY int
}

func (h *Home) tileWidth() int {
	inner := 17 // fits "App Configuration"
	for _, m := range modules.All() {
		if w := runewidth.StringWidth(m.Title()); w > inner {
			inner = w
		}
	}
	return inner + 6 // padding + borders
}

// --- input -----------------------------------------------------------------

func (h *Home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.width, h.height = msg.Width, msg.Height
		return h, nil

	case resourcesLoadedMsg:
		h.loading = false
		h.err = nil
		h.resources = msg.resources
		h.resolveRecents()
		return h, ui.Status("%d resources discovered", len(msg.resources))

	case discoverErrMsg:
		h.loading = false
		h.err = msg.err
		return h, nil

	case ui.ActivatedMsg:
		if h.loading {
			// Our discovery result may have been delivered to (and ignored
			// by) the view that was on top — start over.
			return h, tea.Batch(h.spin.Tick, h.discover())
		}
		h.resolveRecents()
		return h, nil

	case spinner.TickMsg:
		if !h.loading {
			return h, nil
		}
		var cmd tea.Cmd
		h.spin, cmd = h.spin.Update(msg)
		return h, cmd

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			return h, h.click(msg.X, msg.Y)
		}
		return h, nil

	case tea.KeyMsg:
		return h, h.handleKey(msg.String())
	}
	return h, nil
}

func (h *Home) handleKey(key string) tea.Cmd {
	mods := modules.All()
	perRow := max(1, h.lay.perRow)
	switch key {
	case "enter":
		if h.zone == zoneRecents {
			return h.openRecent(h.recIdx)
		}
		return h.openTile()
	case "l", "right":
		if h.zone == zoneTiles {
			h.tileIdx = (h.tileIdx + 1) % len(mods)
		}
	case "h", "left":
		if h.zone == zoneTiles {
			h.tileIdx = (h.tileIdx + len(mods) - 1) % len(mods)
		}
	case "j", "down":
		if h.zone == zoneTiles {
			if h.tileIdx+perRow < len(mods) {
				h.tileIdx += perRow
			} else if len(h.recents) > 0 {
				h.zone = zoneRecents
				h.recIdx = 0
			}
		} else if h.recIdx < len(h.recents)-1 {
			h.recIdx++
		}
	case "k", "up":
		if h.zone == zoneRecents {
			if h.recIdx > 0 {
				h.recIdx--
			} else {
				h.zone = zoneTiles
			}
		} else if h.tileIdx-perRow >= 0 {
			h.tileIdx -= perRow
		}
	case "tab":
		if h.zone == zoneTiles && len(h.recents) > 0 {
			h.zone = zoneRecents
		} else {
			h.zone = zoneTiles
		}
	case "g":
		h.zone = zoneTiles
		h.tileIdx = 0
	case "R":
		h.loading = true
		return tea.Batch(h.spin.Tick, h.discover())
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n, _ := strconv.Atoi(key)
		return h.openRecent(n - 1)
	}
	return nil
}

func (h *Home) click(x, y int) tea.Cmd {
	mods := modules.All()
	l := h.lay

	if y >= l.tilesY && y < l.tilesY+l.tileRows*tileHeight {
		row := (y - l.tilesY) / tileHeight
		rel := x - l.offsetX - 1
		if rel < 0 {
			return nil
		}
		col := rel / (l.tileW + 2)
		if rel%(l.tileW+2) >= l.tileW || col >= l.perRow {
			return nil // click landed in the gap between tiles
		}
		idx := row*l.perRow + col
		if idx < len(mods) {
			h.zone = zoneTiles
			h.tileIdx = idx
			return h.openTile()
		}
		return nil
	}

	if len(h.recents) > 0 && y >= l.recentsY && y < l.recentsY+len(h.recents) {
		h.zone = zoneRecents
		h.recIdx = y - l.recentsY
		return h.openRecent(h.recIdx)
	}
	return nil
}

// --- rendering ---------------------------------------------------------------

func (h *Home) renderTile(mod modules.Module, selected bool, innerW int) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorSubtle).
		Padding(0, 1)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorText)
	if selected {
		border = border.BorderForeground(ui.ColorAccent)
		nameStyle = nameStyle.Foreground(ui.ColorAccent)
	}
	count := "…"
	switch {
	case h.err != nil:
		count = "?"
	case !h.loading:
		count = strconv.Itoa(h.countFor(mod)) + " resources"
	}
	content := lipgloss.PlaceHorizontal(innerW, lipgloss.Center, mod.Icon()) + "\n" +
		lipgloss.PlaceHorizontal(innerW, lipgloss.Center, nameStyle.Render(mod.Title())) + "\n" +
		lipgloss.PlaceHorizontal(innerW, lipgloss.Center, ui.DimStyle.Render(count))
	return border.Render(content)
}

func (h *Home) tilesBlock(tileW, perRow int) string {
	mods := modules.All()
	innerW := tileW - 4
	var rows []string
	for start := 0; start < len(mods); start += perRow {
		end := min(start+perRow, len(mods))
		joined := ""
		for i := start; i < end; i++ {
			tile := h.renderTile(mods[i], h.zone == zoneTiles && i == h.tileIdx, innerW)
			if joined == "" {
				joined = tile
			} else {
				joined = lipgloss.JoinHorizontal(lipgloss.Top, joined, "  ", tile)
			}
		}
		rows = append(rows, lipgloss.NewStyle().MarginLeft(1).Render(joined))
	}
	return strings.Join(rows, "\n")
}

func (h *Home) recentsBlock(contentW int) string {
	if len(h.recents) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, ui.TableHeaderStyle.Render(" RECENT"))
	nameW := min(40, max(20, contentW/3))
	kindW := 20
	rgW := max(10, contentW-nameW-kindW-9)
	for i, r := range h.recents {
		kind := r.Type
		if m := modules.ForType(r.Type); m != nil {
			kind = m.Icon() + " " + m.Title()
		}
		line := " " + strconv.Itoa(i+1) + "  " +
			runewidth.FillRight(runewidth.Truncate(r.Name, nameW, "…"), nameW) + "  " +
			runewidth.FillRight(runewidth.Truncate(kind, kindW, "…"), kindW) + "  " +
			runewidth.FillRight(runewidth.Truncate(r.ResourceGroup, rgW, "…"), rgW)
		if h.zone == zoneRecents && i == h.recIdx {
			lines = append(lines, ui.SelectedRowStyle.Render(line))
		} else {
			lines = append(lines, ui.NormalRowStyle.Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

// changesBlock shows mutations made through aztui this session, newest
// first. Display only — it isn't focusable.
func (h *Home) changesBlock(contentW int) string {
	all := ui.Changes()
	if len(all) == 0 {
		return ""
	}
	const maxShown = 5
	var lines []string
	lines = append(lines, ui.TableHeaderStyle.Render(" CHANGES THIS SESSION"))
	scopeW := min(28, max(14, contentW/4))
	actionW := max(16, contentW-scopeW-12)
	for i, c := range all {
		if i >= maxShown {
			lines = append(lines, ui.DimStyle.Render(fmt.Sprintf("   … and %d more", len(all)-maxShown)))
			break
		}
		lines = append(lines,
			ui.DimStyle.Render(" "+c.At.Format("15:04:05")+"  ")+
				ui.WarnStyle.Render(runewidth.FillRight(runewidth.Truncate(c.Scope, scopeW, "…"), scopeW))+
				ui.NormalRowStyle.Render("  "+runewidth.Truncate(c.Action, actionW, "…")))
	}
	return strings.Join(lines, "\n")
}

func (h *Home) View() string {
	tileW := h.tileWidth()
	mods := modules.All()
	perRow := max(1, (h.width-2)/(tileW+2))
	if perRow > len(mods) {
		perRow = len(mods)
	}
	tileRows := (len(mods) + perRow - 1) / perRow

	tiles := h.tilesBlock(tileW, perRow)
	contentW := lipgloss.Width(tiles)
	recents := h.recentsBlock(contentW)
	changes := h.changesBlock(contentW)

	var tail string
	switch {
	case h.loading:
		tail = "\n\n " + h.spin.View() + ui.DimStyle.Render(" discovering resources across your subscriptions...")
	case h.err != nil:
		tail = "\n\n" + ui.ErrStyle.Render(" discovery failed: "+h.err.Error()) + "\n" +
			ui.DimStyle.Render(" press R to retry — is your `az login` still valid?")
	}

	totalH := tileRows * tileHeight
	if recents != "" {
		totalH += 1 + lipgloss.Height(recents)
	}
	if changes != "" {
		totalH += 1 + lipgloss.Height(changes)
	}
	if tail != "" {
		totalH += 2
	}

	offsetX := max(0, (h.width-contentW)/2)
	offsetY := max(0, (h.height-totalH)/2)

	// Record geometry for mouse hit-testing before applying margins.
	h.lay = homeLayout{
		offsetX:  offsetX,
		tilesY:   offsetY,
		tileW:    tileW,
		perRow:   perRow,
		tileRows: tileRows,
		recentsY: offsetY + tileRows*tileHeight + 2,
	}

	body := tiles
	if recents != "" {
		body += "\n\n" + recents
	}
	if changes != "" {
		body += "\n\n" + changes
	}
	body += tail

	indented := lipgloss.NewStyle().MarginLeft(offsetX).Render(body)
	return strings.Repeat("\n", offsetY) + indented
}

func (h *Home) Breadcrumb() string { return "" }

func (h *Home) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "h/l", Desc: "choose module tile"},
		{Keys: "j/k", Desc: "move into recents"},
		{Keys: "enter", Desc: "open selection"},
		{Keys: "1-9", Desc: "open recent by number"},
		{Keys: "click", Desc: "tiles and recents are clickable"},
		{Keys: "R", Desc: "re-discover resources"},
	}
}
