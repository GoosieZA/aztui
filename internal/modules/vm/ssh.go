package vm

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GoosieZA/aztui/internal/modules"
	"github.com/GoosieZA/aztui/internal/ui"
)

type sshDoneMsg struct{ err error }

type ipChoice struct {
	label string
	addr  string
}

const (
	fieldIP = iota
	fieldUser
	fieldAuth
	fieldKeyPath
)

var authModes = []string{"default keys / agent", "key file", "password (ssh prompts)"}

// sshView collects the connection parameters and hands the terminal to a
// real ssh process. Passwords are never typed into aztui — ssh itself
// prompts inside the suspended session.
type sshView struct {
	mctx   modules.Context
	vmName string

	ips     []ipChoice
	ipIdx   int
	user    textinput.Model
	authIdx int
	keyPath textinput.Model

	focus int

	width, height int
}

func newSSHView(mctx modules.Context, vmName string, privateIPs, publicIPs []string) *sshView {
	var ips []ipChoice
	for _, ip := range privateIPs {
		ips = append(ips, ipChoice{label: "private " + ip, addr: ip})
	}
	for _, ip := range publicIPs {
		ips = append(ips, ipChoice{label: "public " + ip, addr: ip})
	}

	user := textinput.New()
	user.Placeholder = "azureuser"
	user.SetValue(mctx.Config.SSH.User)
	user.Width = 30

	keyPath := textinput.New()
	keyPath.Placeholder = "~/.ssh/id_rsa"
	keyPath.SetValue(mctx.Config.SSH.KeyPath)
	keyPath.Width = 50

	v := &sshView{mctx: mctx, vmName: vmName, ips: ips, user: user, keyPath: keyPath, focus: fieldUser}
	if mctx.Config.SSH.KeyPath != "" {
		v.authIdx = 1
	}
	v.user.Focus()
	return v
}

func (v *sshView) Init() tea.Cmd { return textinput.Blink }

func (v *sshView) setFocus(f int) tea.Cmd {
	if f == fieldKeyPath && v.authIdx != 1 {
		if v.focus < f {
			f = fieldIP // wrap past the hidden field
		} else {
			f = fieldAuth
		}
	}
	v.focus = f
	v.user.Blur()
	v.keyPath.Blur()
	switch f {
	case fieldUser:
		return v.user.Focus()
	case fieldKeyPath:
		return v.keyPath.Focus()
	}
	return nil
}

func (v *sshView) connect() tea.Cmd {
	if len(v.ips) == 0 {
		return ui.Errorf("no IP address to connect to")
	}
	user := strings.TrimSpace(v.user.Value())
	if user == "" {
		return ui.Errorf("username is required")
	}
	target := user + "@" + v.ips[v.ipIdx].addr

	args := []string{}
	switch v.authIdx {
	case 1:
		key := strings.TrimSpace(v.keyPath.Value())
		if key == "" {
			return ui.Errorf("key path is required for key-file auth")
		}
		if strings.HasPrefix(key, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				key = home + key[1:]
			}
		}
		args = append(args, "-i", key)
	case 2:
		// Honor the explicit password choice even when keys would match.
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "PubkeyAuthentication=no")
	}
	args = append(args, target)

	// Remember what worked-ish for next time.
	v.mctx.Config.SSH.User = user
	if v.authIdx == 1 {
		v.mctx.Config.SSH.KeyPath = strings.TrimSpace(v.keyPath.Value())
	}
	_ = v.mctx.Config.Save()

	cmd := exec.Command("ssh", args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return sshDoneMsg{err: err} })
}

func (v *sshView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		return v, nil

	case sshDoneMsg:
		if msg.err != nil {
			return v, ui.Warnf("ssh exited: %v", msg.err)
		}
		return v, ui.Status("ssh session to %s ended", v.vmName)

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return v, ui.Pop()
		case "enter":
			return v, v.connect()
		case "tab", "down":
			return v, v.setFocus((v.focus + 1) % 4)
		case "shift+tab", "up":
			return v, v.setFocus((v.focus + 3) % 4)
		case "left", "right", "h", "l":
			vi := msg.String() == "h" || msg.String() == "l"
			editing := v.focus == fieldUser || v.focus == fieldKeyPath
			if vi && editing {
				break // h/l are normal characters inside text fields
			}
			delta := 1
			if msg.String() == "left" || msg.String() == "h" {
				delta = -1
			}
			switch v.focus {
			case fieldIP:
				if n := len(v.ips); n > 0 {
					v.ipIdx = (v.ipIdx + delta + n) % n
				}
				return v, nil
			case fieldAuth:
				n := len(authModes)
				v.authIdx = (v.authIdx + delta + n) % n
				return v, nil
			}
		}
		var cmd tea.Cmd
		switch v.focus {
		case fieldUser:
			v.user, cmd = v.user.Update(msg)
		case fieldKeyPath:
			v.keyPath, cmd = v.keyPath.Update(msg)
		}
		return v, cmd
	}
	return v, nil
}

func (v *sshView) line(field int, label, value string) string {
	marker := "  "
	style := ui.DimStyle
	if v.focus == field {
		marker = ui.TitleStyle.Render("▸ ")
		style = ui.TitleStyle
	}
	return marker + style.Render(fmt.Sprintf("%-9s", label)) + " " + value
}

func (v *sshView) View() string {
	var b strings.Builder
	b.WriteString("\n " + ui.TitleStyle.Render("ssh → "+v.vmName) + "\n\n")

	ip := "no addresses found"
	if len(v.ips) > 0 {
		var parts []string
		for i, c := range v.ips {
			if i == v.ipIdx {
				parts = append(parts, ui.SelectedRowStyle.Render(" "+c.label+" "))
			} else {
				parts = append(parts, ui.DimStyle.Render(" "+c.label+" "))
			}
		}
		ip = strings.Join(parts, " ")
	}
	b.WriteString(" " + v.line(fieldIP, "address", ip) + "\n")
	b.WriteString(" " + v.line(fieldUser, "user", v.user.View()) + "\n")

	var auths []string
	for i, a := range authModes {
		if i == v.authIdx {
			auths = append(auths, ui.SelectedRowStyle.Render(" "+a+" "))
		} else {
			auths = append(auths, ui.DimStyle.Render(" "+a+" "))
		}
	}
	b.WriteString(" " + v.line(fieldAuth, "auth", strings.Join(auths, " ")) + "\n")
	if v.authIdx == 1 {
		b.WriteString(" " + v.line(fieldKeyPath, "key file", v.keyPath.View()) + "\n")
	}

	b.WriteString("\n " + ui.DimStyle.Render("tab/↑↓ fields · ←/→ choices · enter connect · esc cancel") + "\n")
	return lipgloss.NewStyle().MarginLeft(max(0, (v.width-70)/2)).Render(b.String())
}

// InputActive keeps the app shell from stealing esc and ':' while typing.
func (v *sshView) InputActive() bool { return true }

func (v *sshView) Breadcrumb() string { return "ssh" }

func (v *sshView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "tab/↑↓", Desc: "move between fields"},
		{Keys: "←/→", Desc: "pick address / auth"},
		{Keys: "enter", Desc: "connect"},
		{Keys: "esc", Desc: "cancel"},
	}
}
