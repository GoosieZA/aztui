// Package app is aztui's root Bubble Tea model: it owns the navigation
// stack, the status bar, the `:` command line, and the help overlay, and
// routes everything else to the active view.
package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

// heartbeatInterval drives toast expiry and the activity panel animation.
const heartbeatInterval = 300 * time.Millisecond

var logoLines = []string{
	` ████  █████ █████ █   █ ███`,
	`█    █    █    █   █   █  █ `,
	`██████   █     █   █   █  █ `,
	`█    █  █      █   █   █  █ `,
	`█    █ █████   █    ███  ███`,
}

type heartbeatMsg time.Time

func heartbeat() tea.Cmd {
	return tea.Tick(heartbeatInterval, func(t time.Time) tea.Msg { return heartbeatMsg(t) })
}

// toast is one notification popup, shown top-right until it expires.
type toast struct {
	text  string
	level ui.StatusLevel
	until time.Time
}

type Model struct {
	mctx     modules.Context
	authDesc string

	stack []tea.Model

	cmdline   textinput.Model
	cmdActive bool

	toasts []toast

	showHelp      bool
	width, height int
}

func New(mctx modules.Context, authDesc string) *Model {
	cmd := textinput.New()
	cmd.Prompt = ":"
	cmd.PromptStyle = ui.TitleStyle
	return &Model{
		mctx:     mctx,
		authDesc: authDesc,
		cmdline:  cmd,
		stack:    []tea.Model{NewHome(mctx)},
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.stack[0].Init(), heartbeat())
}

func (m *Model) top() tea.Model { return m.stack[len(m.stack)-1] }

// headerHeight is the static top chrome: logo + context-sensitive keys
// panel. Hidden on small terminals to preserve working space.
func (m *Model) headerHeight() int {
	if m.height >= 24 && m.width >= 80 {
		return len(logoLines) + 1 // keys panel is one taller than the logo
	}
	return 0
}

func (m *Model) bodyHeight() int { return max(1, m.height-1-m.headerHeight()) }

// sizeFor computes a view's window: home draws edge-to-edge, every other
// view sits inside a one-cell border frame.
func (m *Model) sizeFor(v tea.Model) tea.WindowSizeMsg {
	if _, ok := v.(*Home); ok {
		return tea.WindowSizeMsg{Width: m.width, Height: m.bodyHeight()}
	}
	return tea.WindowSizeMsg{Width: max(1, m.width-2), Height: max(1, m.bodyHeight()-2)}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.cmdline.Width = msg.Width - 2
		var cmds []tea.Cmd
		for i, v := range m.stack {
			updated, cmd := v.Update(m.sizeFor(v))
			m.stack[i] = updated
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case ui.PushViewMsg:
		m.stack = append(m.stack, msg.Model)
		updated, _ := msg.Model.Update(m.sizeFor(msg.Model))
		m.stack[len(m.stack)-1] = updated
		return m, updated.Init()

	case ui.PopViewMsg:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		_, cmd1 := m.forward(ui.ActivatedMsg{})
		var cmd2 tea.Cmd
		if msg.Result != nil {
			_, cmd2 = m.forward(msg.Result)
		}
		return m, tea.Batch(cmd1, cmd2)

	case ui.StatusMsg:
		ttl := 5 * time.Second
		if msg.Level == ui.StatusError {
			ttl = 8 * time.Second
		}
		m.toasts = append(m.toasts, toast{text: msg.Text, level: msg.Level, until: time.Now().Add(ttl)})
		if len(m.toasts) > 4 {
			m.toasts = m.toasts[len(m.toasts)-4:]
		}
		return m, nil

	case heartbeatMsg:
		now := time.Time(msg)
		kept := m.toasts[:0]
		for _, t := range m.toasts {
			if t.until.After(now) {
				kept = append(kept, t)
			}
		}
		m.toasts = kept
		return m, heartbeat()

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Translate screen coordinates into the active view's space: the
		// header sits above the body, and framed views have a border cell.
		msg.Y -= m.headerHeight()
		if _, isHome := m.top().(*Home); !isHome {
			msg.X--
			msg.Y--
		}
		if msg.Y < 0 && msg.Action == tea.MouseActionPress {
			return m, nil // clicks on the header chrome
		}
		return m.forward(msg)
	}

	return m.forward(msg)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	if m.cmdActive {
		switch key {
		case "esc":
			m.cmdActive = false
			m.cmdline.SetValue("")
		case "enter":
			cmd := strings.TrimSpace(m.cmdline.Value())
			m.cmdActive = false
			m.cmdline.SetValue("")
			return m, m.runCommand(cmd)
		default:
			var cmd tea.Cmd
			m.cmdline, cmd = m.cmdline.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	inputActive := false
	if ia, ok := m.top().(ui.InputActiver); ok {
		inputActive = ia.InputActive()
	}
	if !inputActive {
		switch key {
		case ":":
			m.cmdActive = true
			return m, m.cmdline.Focus()
		case "?":
			m.showHelp = true
			return m, nil
		case "esc":
			if len(m.stack) > 1 {
				popped := m.top()
				m.stack = m.stack[:len(m.stack)-1]
				_, cmd1 := m.forward(ui.ActivatedMsg{})
				var cmd2 tea.Cmd
				if p, ok := popped.(ui.PopResulter); ok {
					if result := p.PopResult(); result != nil {
						_, cmd2 = m.forward(result)
					}
				}
				return m, tea.Batch(cmd1, cmd2)
			}
		}
	}

	return m.forward(msg)
}

func (m *Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.top().Update(msg)
	m.stack[len(m.stack)-1] = updated
	return m, cmd
}

func (m *Model) runCommand(cmd string) tea.Cmd {
	switch cmd {
	case "":
		return nil
	case "q", "quit", "exit":
		return tea.Quit
	case "home":
		home := NewHome(m.mctx)
		m.stack = []tea.Model{home}
		updated, _ := home.Update(tea.WindowSizeMsg{Width: m.width, Height: m.bodyHeight()})
		m.stack[0] = updated
		return m.stack[0].Init()
	case "help":
		m.showHelp = true
		return nil
	case "ro", "readonly":
		ui.SetReadOnly(!ui.IsReadOnly())
		if ui.IsReadOnly() {
			return ui.Warnf("read-only mode ON — mutations disabled")
		}
		return ui.Status("read-only mode off")
	}
	if mod := modules.Find(cmd); mod != nil {
		return ui.Push(newResourcesView(m.mctx, mod, nil))
	}
	return ui.Errorf("unknown command %q — try :home, :q, or a module name", cmd)
}

func (m *Model) breadcrumb() string {
	parts := []string{"aztui"}
	for _, v := range m.stack {
		if b, ok := v.(ui.Breadcrumber); ok {
			if seg := b.Breadcrumb(); seg != "" {
				parts = append(parts, seg)
			}
		}
	}
	return strings.Join(parts, " › ")
}

func (m *Model) statusBar() string {
	if m.cmdActive {
		return ui.StatusBarStyle.Width(m.width).Render(" " + m.cmdline.View())
	}

	left := " " + ui.BreadcrumbStyle.Render(m.breadcrumb())

	right := ""
	if ui.IsReadOnly() {
		right = ui.WarnStyle.Bold(true).Render("[READ-ONLY] ")
	}
	right += ui.StatusHintStyle.Render("?:help ::cmd /:filter ") +
		ui.DimStyle.Render("⚿ "+m.authDesc+" ")

	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		return ui.StatusBarStyle.Width(m.width).Render(left)
	}
	return ui.StatusBarStyle.Render(left + strings.Repeat(" ", pad) + right)
}

// header renders the persistent top chrome: the logo on the left, the
// context-sensitive keys panel, and — while background work is running — an
// animated activity box on the right.
func (m *Model) header() string {
	if m.headerHeight() == 0 {
		return ""
	}
	logo := lipgloss.NewStyle().Margin(0, 2, 0, 1).Render(
		ui.LogoStyle.Render(strings.Join(logoLines, "\n")))
	panelW := m.width - lipgloss.Width(logo) - 1

	const activityW = 42
	if ops := ui.Ops(); len(ops) > 0 && panelW > activityW+34 {
		activity := m.activityBox(activityW, ops)
		return lipgloss.JoinHorizontal(lipgloss.Center, logo, m.keysPanel(panelW-activityW), activity)
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, logo, m.keysPanel(panelW))
}

// activityBox lists running background operations with a sweeping progress
// animation and elapsed time, k9s-pulse style.
func (m *Model) activityBox(width int, ops []ui.Op) string {
	const rows = 4
	now := time.Now()
	innerW := width - 4
	var lines []string
	for i, op := range ops {
		if i == rows-1 && len(ops) > rows {
			lines = append(lines, ui.DimStyle.Render(fmt.Sprintf("… and %d more", len(ops)-i)))
			break
		}
		if i >= rows {
			break
		}
		elapsed := now.Sub(op.Started)
		clock := fmt.Sprintf("%02d:%02d", int(elapsed.Minutes()), int(elapsed.Seconds())%60)
		labelW := innerW - 8 - 1 - len(clock) - 1
		label := runewidth.FillRight(runewidth.Truncate(op.Label, max(4, labelW), "…"), max(4, labelW))
		lines = append(lines, sweepBar(now, 8)+" "+ui.HelpDescStyle.Render(label)+" "+ui.DimStyle.Render(clock))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorWarn).
		Padding(0, 1).
		Width(width - 2).
		Render(strings.Join(lines, "\n"))
}

// sweepBar renders an indeterminate progress bar: a 3-cell window sweeping
// across w cells, advancing with wall-clock time.
func sweepBar(now time.Time, w int) string {
	pos := int(now.UnixMilli()/150) % w
	var b strings.Builder
	for i := 0; i < w; i++ {
		if (i-pos+w)%w < 3 {
			b.WriteString(ui.TitleStyle.Render("▰"))
		} else {
			b.WriteString(ui.DimStyle.Render("▱"))
		}
	}
	return b.String()
}

// toastsView stacks active notifications, newest at the top.
func (m *Model) toastsView() string {
	if len(m.toasts) == 0 {
		return ""
	}
	var boxes []string
	for i := len(m.toasts) - 1; i >= 0; i-- {
		t := m.toasts[i]
		icon, color := "✓", ui.ColorOK
		switch t.level {
		case ui.StatusWarn:
			icon, color = "⚠", ui.ColorWarn
		case ui.StatusError:
			icon, color = "✗", ui.ColorError
		}
		text := runewidth.Truncate(t.text, max(10, m.width/2), "…")
		boxes = append(boxes, lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(color).
			Padding(0, 1).
			Render(lipgloss.NewStyle().Foreground(color).Render(icon)+" "+text))
	}
	return strings.Join(boxes, "\n")
}

// overlayTopRight composites overlay onto the top-right corner of body.
// Each overlay line is right-aligned independently, ANSI-aware.
func overlayTopRight(body, overlay string, width int) string {
	if overlay == "" {
		return body
	}
	bodyLines := strings.Split(body, "\n")
	for i, ov := range strings.Split(overlay, "\n") {
		ovW := ansi.StringWidth(ov)
		if ovW >= width {
			continue
		}
		for len(bodyLines) <= i {
			bodyLines = append(bodyLines, "")
		}
		keep := width - ovW - 1
		line := ansi.Truncate(bodyLines[i], keep, "")
		pad := keep - ansi.StringWidth(line)
		if pad < 0 {
			pad = 0
		}
		bodyLines[i] = line + strings.Repeat(" ", pad) + ov
	}
	return strings.Join(bodyLines, "\n")
}

// keysPanel lays out the active view's keybindings (plus a few globals) in
// columns, k9s-style.
func (m *Model) keysPanel(width int) string {
	var hints []ui.KeyHint
	if kh, ok := m.top().(ui.KeyHinter); ok {
		hints = append(hints, kh.KeyHints()...)
	}
	if len(m.stack) > 1 {
		hints = append(hints, ui.KeyHint{Keys: "esc", Desc: "back"})
	}
	hints = append(hints,
		ui.KeyHint{Keys: ":", Desc: "command"},
		ui.KeyHint{Keys: "?", Desc: "help"},
		ui.KeyHint{Keys: "ctrl+c", Desc: "quit"},
	)

	const rows = 4
	contentW := max(10, width-4) // borders + padding
	lines := make([]string, rows)
	used := 0
	for c := 0; c*rows < len(hints); c++ {
		col := hints[c*rows : min((c+1)*rows, len(hints))]
		keyW, descW := 0, 0
		for _, h := range col {
			keyW = max(keyW, runewidth.StringWidth(h.Keys))
			descW = max(descW, runewidth.StringWidth(h.Desc))
		}
		colW := keyW + 1 + descW + 3
		if used+colW > contentW {
			break
		}
		used += colW
		for r := 0; r < rows; r++ {
			cell := strings.Repeat(" ", colW)
			if r < len(col) {
				cell = ui.HelpKeyStyle.Render(runewidth.FillRight(col[r].Keys, keyW)) + " " +
					ui.HelpDescStyle.Render(runewidth.FillRight(col[r].Desc, descW)) + "   "
			}
			lines[r] += cell
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorSubtle).
		Padding(0, 1).
		Width(max(1, width-2)).
		Render(strings.TrimRight(strings.Join(lines, "\n"), "\n"))
}

// framed wraps a view in a rounded border whose top edge carries the title.
func (m *Model) framed(content, title string) string {
	innerW := max(1, m.width-2)
	innerH := max(1, m.bodyHeight()-2)
	body := lipgloss.Place(innerW, innerH, lipgloss.Left, lipgloss.Top, content)
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(ui.ColorBlue).
		Render(body)

	line := lipgloss.NewStyle().Foreground(ui.ColorBlue)
	titleText := ""
	if title != "" {
		titleText = " " + runewidth.Truncate(title, max(1, innerW-6), "…") + " "
	}
	dashes := innerW - 1 - runewidth.StringWidth(titleText)
	top := line.Render("╭─") + ui.TitleStyle.Render(titleText) +
		line.Render(strings.Repeat("─", max(0, dashes))+"╮")
	return top + "\n" + frame
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	var body string
	switch {
	case m.showHelp:
		title, hints := "View", []ui.KeyHint(nil)
		if h, ok := m.top().(ui.KeyHinter); ok {
			hints = h.KeyHints()
		}
		if b, ok := m.top().(ui.Breadcrumber); ok {
			title = b.Breadcrumb()
		}
		body = ui.RenderHelp(m.width, m.bodyHeight(), title, hints)
	default:
		if _, isHome := m.top().(*Home); isHome {
			body = lipgloss.Place(m.width, m.bodyHeight(), lipgloss.Left, lipgloss.Top, m.top().View())
		} else {
			title := ""
			if b, ok := m.top().(ui.Breadcrumber); ok {
				title = b.Breadcrumb()
			}
			body = m.framed(m.top().View(), title)
		}
	}

	body = overlayTopRight(body, m.toastsView(), m.width)

	if header := m.header(); header != "" {
		return header + "\n" + body + "\n" + m.statusBar()
	}
	return body + "\n" + m.statusBar()
}
