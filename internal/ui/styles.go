// Package ui contains aztui's shared terminal UI building blocks: theme,
// vi-key table, $EDITOR integration, confirm prompts, and help rendering.
package ui

import "github.com/charmbracelet/lipgloss"

// Theme palette — Azure-leaning blues with high-contrast accents.
var (
	ColorAccent  = lipgloss.Color("45")  // bright cyan
	ColorBlue    = lipgloss.Color("33")  // azure blue
	ColorText    = lipgloss.Color("252") // near-white
	ColorDim     = lipgloss.Color("241") // gray
	ColorSubtle  = lipgloss.Color("238")
	ColorWarn    = lipgloss.Color("214") // orange
	ColorError   = lipgloss.Color("203") // red
	ColorOK      = lipgloss.Color("78")  // green
	ColorSelBG   = lipgloss.Color("24")  // deep blue selection
	ColorSelText = lipgloss.Color("231")
)

var (
	LogoStyle     = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	SubtitleStyle = lipgloss.NewStyle().Foreground(ColorDim).Italic(true)

	TitleStyle = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	DimStyle   = lipgloss.NewStyle().Foreground(ColorDim)
	OKStyle    = lipgloss.NewStyle().Foreground(ColorOK)
	WarnStyle  = lipgloss.NewStyle().Foreground(ColorWarn)
	ErrStyle   = lipgloss.NewStyle().Foreground(ColorError)

	TableHeaderStyle = lipgloss.NewStyle().Foreground(ColorBlue).Bold(true)
	SelectedRowStyle = lipgloss.NewStyle().Foreground(ColorSelText).Background(ColorSelBG).Bold(true)
	NormalRowStyle   = lipgloss.NewStyle().Foreground(ColorText)
	RecentMarkStyle  = lipgloss.NewStyle().Foreground(ColorWarn)

	StatusBarStyle  = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(ColorText)
	BreadcrumbStyle = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	StatusHintStyle = lipgloss.NewStyle().Foreground(ColorDim)

	DialogStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorAccent).
			Padding(0, 2)

	ConfirmStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorWarn).
			Padding(0, 2)

	HelpKeyStyle  = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	HelpDescStyle = lipgloss.NewStyle().Foreground(ColorText)
)
