package ui

import (
	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

// Yank copies a value to the system clipboard and toasts a confirmation —
// the platform-wide `y` behavior.
func Yank(label, value string) tea.Cmd {
	if value == "" {
		return Warnf("nothing to yank")
	}
	if err := clipboard.WriteAll(value); err != nil {
		return Errorf("clipboard: %v", err)
	}
	return Status("yanked %s (%d chars)", label, len(value))
}
