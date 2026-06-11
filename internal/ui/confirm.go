package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Confirm is a y/N prompt a view embeds for destructive actions. While
// active it swallows all keys; the view should call Update first and bail out
// when handled.
type Confirm struct {
	Active  bool
	Tag     string
	Payload any
	prompt  string
}

// ConfirmResult is returned from Update when the user answers.
type ConfirmResult struct {
	Tag     string
	OK      bool
	Payload any
}

func (c *Confirm) Ask(tag, prompt string, payload any) {
	c.Active = true
	c.Tag = tag
	c.prompt = prompt
	c.Payload = payload
}

// Update consumes a key when the prompt is active. handled reports whether
// the message was swallowed; result is non-nil once the user has answered.
func (c *Confirm) Update(msg tea.Msg) (handled bool, result *ConfirmResult) {
	if !c.Active {
		return false, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch keyMsg.String() {
	case "y", "Y":
		c.Active = false
		return true, &ConfirmResult{Tag: c.Tag, OK: true, Payload: c.Payload}
	case "n", "N", "esc", "enter":
		c.Active = false
		return true, &ConfirmResult{Tag: c.Tag, OK: false, Payload: c.Payload}
	}
	return true, nil
}

// Overlay renders the prompt centered in a box of the given dimensions, on
// top of nothing — callers show it instead of their normal body.
func (c *Confirm) Overlay(width, height int) string {
	box := ConfirmStyle.Render(
		WarnStyle.Bold(true).Render(c.prompt) + "\n\n" +
			HelpKeyStyle.Render("y") + HelpDescStyle.Render(" confirm   ") +
			HelpKeyStyle.Render("n/esc") + HelpDescStyle.Render(" cancel"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
