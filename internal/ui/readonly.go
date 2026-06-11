package ui

import (
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
)

var readOnly atomic.Bool

// SetReadOnly switches the global read-only mode: when on, views refuse
// every mutating action so production can be browsed without risk.
func SetReadOnly(v bool) { readOnly.Store(v) }

func IsReadOnly() bool { return readOnly.Load() }

// BlockIfReadOnly returns a warning command when mutations are disabled, or
// nil when the action may proceed. Destructive key handlers call this first:
//
//	if cmd := ui.BlockIfReadOnly(); cmd != nil { return cmd, true }
func BlockIfReadOnly() tea.Cmd {
	if readOnly.Load() {
		return Warnf("read-only mode — :ro to allow changes")
	}
	return nil
}
