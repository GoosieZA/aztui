package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Column describes one table column. Width pins an exact width; otherwise the
// remaining space is shared proportionally by Weight.
type Column struct {
	Title  string
	Weight int
	Width  int
}

// Table is a vi-key navigable table with a `/` filter, in the spirit of k9s.
// With Selectable set, space toggles row selection for bulk operations.
// It is a component, not a full tea.Model — parents call Update and View.
type Table struct {
	Empty      string // shown when there are no rows
	Selectable bool

	cols     []Column
	rows     [][]string
	visible  []int // indices into rows after filtering
	cursor   int   // index into visible
	offset   int
	selected map[int]bool // original row indices

	filter    textinput.Model
	filtering bool

	width, height int
}

var (
	gridStyle       = lipgloss.NewStyle().Foreground(ColorSubtle)
	selectedGutter  = lipgloss.NewStyle().Foreground(ColorWarn).Bold(true)
	multiRowStyle   = lipgloss.NewStyle().Foreground(ColorWarn)
	columnSeparator = " │ "
)

func NewTable(cols ...Column) Table {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.PromptStyle = TitleStyle
	ti.Placeholder = "filter"
	return Table{cols: cols, filter: ti, Empty: "no results", selected: map[int]bool{}}
}

func (t *Table) SetSize(w, h int) {
	t.width, t.height = w, h
	t.scroll()
}

// SetRows replaces the table contents, keeping the cursor position stable
// where possible. Any multi-selection is cleared: row indices are no longer
// meaningful after a reload.
func (t *Table) SetRows(rows [][]string) {
	t.rows = rows
	t.selected = map[int]bool{}
	t.applyFilter()
}

// CursorRow returns the index into the original rows slice for the selected
// row, or -1 when the table is empty.
func (t *Table) CursorRow() int {
	if len(t.visible) == 0 || t.cursor >= len(t.visible) {
		return -1
	}
	return t.visible[t.cursor]
}

func (t *Table) Count() int { return len(t.visible) }

// GotoBottom moves the cursor to the last row (used by follow modes).
func (t *Table) GotoBottom() {
	t.cursor = len(t.visible) - 1
	t.scroll()
}

// SelectedRows returns the original indices of all multi-selected rows, in
// ascending order.
func (t *Table) SelectedRows() []int {
	out := make([]int, 0, len(t.selected))
	for i := range t.selected {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

func (t *Table) SelectionCount() int { return len(t.selected) }

func (t *Table) ClearSelection() { t.selected = map[int]bool{} }

func (t *Table) InputActive() bool { return t.filtering }

func (t *Table) ResetFilter() {
	t.filter.SetValue("")
	t.filtering = false
	t.filter.Blur()
	t.applyFilter()
}

func (t *Table) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if t.filtering {
		switch keyMsg.String() {
		case "esc":
			t.ResetFilter()
		case "enter":
			t.filtering = false
			t.filter.Blur()
		default:
			var cmd tea.Cmd
			t.filter, cmd = t.filter.Update(msg)
			t.applyFilter()
			return cmd
		}
		return nil
	}

	switch keyMsg.String() {
	case "j", "down":
		t.move(1)
	case "k", "up":
		t.move(-1)
	case "g", "home":
		t.cursor = 0
		t.scroll()
	case "G", "end":
		t.cursor = len(t.visible) - 1
		t.scroll()
	case "ctrl+d", "pgdown":
		t.move(t.pageSize() / 2)
	case "ctrl+u", "pgup":
		t.move(-t.pageSize() / 2)
	case "/":
		t.filtering = true
		return t.filter.Focus()
	case " ":
		if t.Selectable {
			if idx := t.CursorRow(); idx >= 0 {
				if t.selected[idx] {
					delete(t.selected, idx)
				} else {
					t.selected[idx] = true
				}
				t.move(1)
			}
		}
	case "ctrl+a":
		if t.Selectable {
			if len(t.selected) == len(t.visible) {
				t.ClearSelection()
			} else {
				for _, idx := range t.visible {
					t.selected[idx] = true
				}
			}
		}
	}
	return nil
}

func (t *Table) move(delta int) {
	t.cursor += delta
	t.scroll()
}

func (t *Table) scroll() {
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor > len(t.visible)-1 {
		t.cursor = max(0, len(t.visible)-1)
	}
	page := t.pageSize()
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+page {
		t.offset = t.cursor - page + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// pageSize is the number of data rows that fit on screen.
func (t *Table) pageSize() int {
	h := t.height - 2 // header + rule
	if t.filterLineVisible() {
		h--
	}
	return max(1, h)
}

func (t *Table) filterLineVisible() bool {
	return t.filtering || t.filter.Value() != ""
}

func (t *Table) applyFilter() {
	needle := strings.ToLower(t.filter.Value())
	t.visible = t.visible[:0]
	for i, row := range t.rows {
		if needle == "" || strings.Contains(strings.ToLower(strings.Join(row, " ")), needle) {
			t.visible = append(t.visible, i)
		}
	}
	t.scroll()
}

func (t *Table) widths() []int {
	sep := runewidth.StringWidth(columnSeparator)
	avail := t.width - 2 - sep*(len(t.cols)-1) // 1 gutter col + 1 right margin
	weightTotal, fixed := 0, 0
	for _, c := range t.cols {
		if c.Width > 0 {
			fixed += c.Width
		} else {
			weightTotal += c.Weight
		}
	}
	avail -= fixed
	ws := make([]int, len(t.cols))
	used := 0
	lastFlex := -1
	for i, c := range t.cols {
		if c.Width > 0 {
			ws[i] = c.Width
			continue
		}
		ws[i] = max(4, avail*c.Weight/max(1, weightTotal))
		used += ws[i]
		lastFlex = i
	}
	if lastFlex >= 0 && avail-used > 0 {
		ws[lastFlex] += avail - used
	}
	return ws
}

// renderRow draws one row: a 1-column gutter, then cells separated by grid
// lines. Styles are applied per piece so the selection bar can span the full
// row while grid lines stay subtle on unselected rows.
func (t *Table) renderRow(cells []string, ws []int, gutter string, gutterStyle, cellStyle, sepStyle lipgloss.Style) string {
	var b strings.Builder
	b.WriteString(gutterStyle.Render(gutter))
	for i, w := range ws {
		cell := ""
		if i < len(cells) {
			cell = Flatten(cells[i])
		}
		cell = runewidth.FillRight(runewidth.Truncate(cell, w, "…"), w)
		b.WriteString(cellStyle.Render(cell))
		if i < len(ws)-1 {
			b.WriteString(sepStyle.Render(columnSeparator))
		}
	}
	return b.String()
}

// headerRule draws the line under the header, with joints at each separator.
func (t *Table) headerRule(ws []int) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = strings.Repeat("─", w)
	}
	return gridStyle.Render("─" + strings.Join(parts, "─┼─") + "─")
}

func (t *Table) View() string {
	var b strings.Builder

	if t.filterLineVisible() {
		if t.filtering {
			b.WriteString(" " + t.filter.View())
		} else {
			b.WriteString(DimStyle.Render(" /" + t.filter.Value()))
		}
		b.WriteString("\n")
	}

	ws := t.widths()
	titles := make([]string, len(t.cols))
	for i, c := range t.cols {
		titles[i] = c.Title
	}
	b.WriteString(t.renderRow(titles, ws, " ", gridStyle, TableHeaderStyle, gridStyle))
	b.WriteString("\n")
	b.WriteString(t.headerRule(ws))
	b.WriteString("\n")

	if len(t.visible) == 0 {
		b.WriteString(DimStyle.Render(" " + t.Empty))
		return b.String()
	}

	page := t.pageSize()
	end := min(t.offset+page, len(t.visible))
	for vi := t.offset; vi < end; vi++ {
		orig := t.visible[vi]
		row := t.rows[orig]

		gutter, gutterStyle := " ", gridStyle
		cellStyle, sepStyle := NormalRowStyle, gridStyle
		if t.selected[orig] {
			gutter, gutterStyle = "▌", selectedGutter
			cellStyle = multiRowStyle
		}
		if vi == t.cursor {
			gutterStyle = SelectedRowStyle
			cellStyle = SelectedRowStyle
			sepStyle = SelectedRowStyle
			if t.selected[orig] {
				gutter = "▌"
			}
		}
		b.WriteString(t.renderRow(row, ws, gutter, gutterStyle, cellStyle, sepStyle))
		if vi < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
