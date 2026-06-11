package ui

import (
	"bytes"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// EditorResult is delivered to the view that called OpenEditor once the
// user's $EDITOR exits. Canceled is true when the buffer was left unchanged.
type EditorResult struct {
	Tag      string
	Content  []byte
	Canceled bool
	Err      error
}

// OpenEditor suspends the TUI and opens $VISUAL/$EDITOR (default vim) on a
// temp file seeded with initial. ext controls syntax highlighting in the
// editor ("json", "yaml", ...). Tag is echoed back in the result so callers
// can run multiple editor flows.
func OpenEditor(tag string, initial []byte, ext string) tea.Cmd {
	f, err := os.CreateTemp("", "aztui-*."+ext)
	if err != nil {
		return func() tea.Msg { return EditorResult{Tag: tag, Err: err} }
	}
	path := f.Name()
	if _, err := f.Write(initial); err != nil {
		f.Close()
		os.Remove(path)
		return func() tea.Msg { return EditorResult{Tag: tag, Err: err} }
	}
	f.Close()

	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vim"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)

	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		defer os.Remove(path)
		if execErr != nil {
			return EditorResult{Tag: tag, Err: execErr}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return EditorResult{Tag: tag, Err: err}
		}
		if bytes.Equal(content, initial) {
			return EditorResult{Tag: tag, Canceled: true}
		}
		return EditorResult{Tag: tag, Content: content}
	})
}
