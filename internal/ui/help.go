package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// GlobalHints are the keybindings the root app handles for every view.
var GlobalHints = []KeyHint{
	{"j/k", "move down/up"},
	{"g/G", "go to top/bottom"},
	{"ctrl+d/u", "half page down/up"},
	{"/", "filter"},
	{":", "command (:home, :appconfig, :sb, :q)"},
	{"enter", "select / drill in"},
	{"esc", "back"},
	{"?", "toggle help"},
	{"ctrl+c", "quit"},
}

// RenderHelp draws the help overlay centered on screen.
func RenderHelp(width, height int, viewTitle string, viewHints []KeyHint) string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render("Global") + "\n")
	writeHints(&b, GlobalHints)
	if len(viewHints) > 0 {
		b.WriteString("\n" + TitleStyle.Render(viewTitle) + "\n")
		writeHints(&b, viewHints)
	}
	box := DialogStyle.Render(strings.TrimRight(b.String(), "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func writeHints(b *strings.Builder, hints []KeyHint) {
	for _, h := range hints {
		b.WriteString("  " + HelpKeyStyle.Render(runewidth.FillRight(h.Keys, 10)))
		b.WriteString(HelpDescStyle.Render(h.Desc) + "\n")
	}
}
