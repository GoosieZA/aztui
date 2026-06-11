package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// PushViewMsg asks the root app to push a new view onto the navigation stack.
type PushViewMsg struct{ Model tea.Model }

// PopViewMsg asks the root app to pop the current view. A non-nil Result is
// forwarded to the view that becomes active, letting a child hand data (or a
// refresh hint) back to its parent.
type PopViewMsg struct{ Result tea.Msg }

type StatusLevel int

const (
	StatusInfo StatusLevel = iota
	StatusWarn
	StatusError
)

// StatusMsg flashes a message in the status bar for a few seconds.
type StatusMsg struct {
	Text  string
	Level StatusLevel
}

func Push(m tea.Model) tea.Cmd {
	return func() tea.Msg { return PushViewMsg{Model: m} }
}

func Pop() tea.Cmd {
	return func() tea.Msg { return PopViewMsg{} }
}

// PopWith pops the current view and delivers result to the parent view.
func PopWith(result tea.Msg) tea.Cmd {
	return func() tea.Msg { return PopViewMsg{Result: result} }
}

func Status(format string, args ...any) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: fmt.Sprintf(format, args...)} }
}

func Warnf(format string, args ...any) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: fmt.Sprintf(format, args...), Level: StatusWarn} }
}

func Errorf(format string, args ...any) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: fmt.Sprintf(format, args...), Level: StatusError} }
}

// Err flashes an error in the status bar; nil-safe.
func Err(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return Errorf("%v", err)
}

// InputActiver is implemented by views (and components) that may be capturing
// raw keystrokes — e.g. an open filter prompt — so the root app knows not to
// steal keys like ':' and 'esc'.
type InputActiver interface{ InputActive() bool }

// Breadcrumber lets views contribute a segment to the status bar breadcrumb.
type Breadcrumber interface{ Breadcrumb() string }

// PopResulter lets a view hand a message to its parent when the user backs
// out of it with esc (e.g. a refresh hint after mutating state). Return nil
// when there is nothing to deliver.
type PopResulter interface{ PopResult() tea.Msg }

// ActivatedMsg is delivered to a view when it becomes the active (top) view
// again after the view above it was popped. Async results are only routed to
// the top view, so a view whose work completed while it was buried should
// restart that work here.
type ActivatedMsg struct{}

// KeyHint is one entry in the help overlay.
type KeyHint struct {
	Keys string
	Desc string
}

// KeyHinter lets views list their extra keybindings in the help overlay.
type KeyHinter interface{ KeyHints() []KeyHint }
