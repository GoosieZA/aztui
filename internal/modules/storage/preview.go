package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GoosieZA/aztui/internal/ui"
)

const (
	textPreviewBytes = 256 * 1024
	imagePreviewMax  = 20 * 1024 * 1024
)

func previewKind(name, contentType string) string {
	ct := strings.ToLower(contentType)
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(ct, "image/"),
		ext == ".png", ext == ".jpg", ext == ".jpeg", ext == ".gif":
		return "image"
	case strings.HasPrefix(ct, "text/"),
		strings.Contains(ct, "json"), strings.Contains(ct, "xml"),
		strings.Contains(ct, "yaml"), strings.Contains(ct, "javascript"):
		return "text"
	}
	switch ext {
	case ".txt", ".json", ".log", ".csv", ".yaml", ".yml", ".xml", ".md",
		".html", ".js", ".ts", ".sh", ".config", ".cfg", ".ini", ".properties", ".sql":
		return "text"
	}
	return "unknown" // download a head and sniff
}

type previewMsg struct {
	kind      string // "image" | "text" | "binary"
	data      []byte
	truncated bool
	err       error
}

// previewView shows blob content in the terminal: text/JSON directly,
// images as truecolor half-block cells.
type previewView struct {
	client    *azblob.Client
	container string
	blob      blobEntry

	vp      viewport.Model
	spin    spinner.Model
	loading bool

	kind      string
	data      []byte
	truncated bool

	width, height int
}

func newPreviewView(client *azblob.Client, container string, b blobEntry) *previewView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	return &previewView{client: client, container: container, blob: b, spin: sp, loading: true}
}

func (v *previewView) Init() tea.Cmd {
	client, containerName, b := v.client, v.container, v.blob
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		opID := ui.BeginOp("previewing %s", filepath.Base(b.name))
		defer ui.EndOp(opID)
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()

		kind := previewKind(b.name, b.contentType)
		if kind == "image" {
			if b.size > imagePreviewMax {
				return previewMsg{kind: "binary", err: fmt.Errorf("image is %s — too large to preview, press s to download", ui.Bytes(b.size))}
			}
			resp, err := client.DownloadStream(ctx, containerName, b.name, nil)
			if err != nil {
				return previewMsg{err: err}
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return previewMsg{err: err}
			}
			return previewMsg{kind: "image", data: data}
		}

		// Text (or sniff unknown content): fetch a bounded head.
		opts := &azblob.DownloadStreamOptions{Range: blob.HTTPRange{Offset: 0, Count: textPreviewBytes}}
		resp, err := client.DownloadStream(ctx, containerName, b.name, opts)
		if err != nil {
			return previewMsg{err: err}
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, textPreviewBytes))
		if err != nil {
			return previewMsg{err: err}
		}
		truncated := int64(len(data)) < b.size
		if !utf8.Valid(data) {
			return previewMsg{kind: "binary", data: nil, truncated: truncated}
		}
		return previewMsg{kind: "text", data: data, truncated: truncated}
	})
}

func (v *previewView) render() string {
	switch v.kind {
	case "image":
		img, err := renderImage(v.data, max(10, v.width-2), max(5, v.height-3))
		if err != nil {
			return ui.ErrStyle.Render(" cannot decode image: " + err.Error())
		}
		return img
	case "text":
		text := string(v.data)
		if json.Valid(v.data) {
			var out bytes.Buffer
			if err := json.Indent(&out, v.data, "", "  "); err == nil {
				text = out.String()
			}
		}
		if v.truncated {
			text += "\n\n" + ui.WarnStyle.Render(fmt.Sprintf("… truncated at %s of %s — press s to download the rest", ui.Bytes(textPreviewBytes), ui.Bytes(v.blob.size)))
		}
		return text
	default:
		return ui.DimStyle.Render(fmt.Sprintf(" binary content · %s · %s — press s to download", v.blob.contentType, ui.Bytes(v.blob.size)))
	}
}

func (v *previewView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.vp = viewport.New(msg.Width, max(1, msg.Height-2))
		if !v.loading {
			v.vp.SetContent(v.render())
		}
		return v, nil

	case previewMsg:
		v.loading = false
		if msg.err != nil && msg.kind == "" {
			return v, ui.Err(msg.err)
		}
		v.kind, v.data, v.truncated = msg.kind, msg.data, msg.truncated
		v.vp.SetContent(v.render())
		if msg.err != nil {
			return v, ui.Warnf("%v", msg.err)
		}
		return v, nil

	case downloadDoneMsg:
		if msg.err != nil {
			return v, ui.Errorf("download failed: %v", msg.err)
		}
		return v, ui.Status("saved %s (%s)", msg.path, ui.Bytes(msg.size))

	case ui.ActivatedMsg:
		if v.loading {
			return v, v.Init()
		}
		return v, nil

	case spinner.TickMsg:
		if !v.loading {
			return v, nil
		}
		var cmd tea.Cmd
		v.spin, cmd = v.spin.Update(msg)
		return v, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "s":
			return v, tea.Batch(ui.Status("downloading %s...", filepath.Base(v.blob.name)),
				downloadCmd(v.client, v.container, v.blob))
		case "g":
			v.vp.GotoTop()
			return v, nil
		case "G":
			v.vp.GotoBottom()
			return v, nil
		}
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v *previewView) View() string {
	title := ui.TitleStyle.Render(" "+filepath.Base(v.blob.name)) +
		ui.DimStyle.Render(fmt.Sprintf("  %s · %s", ui.Bytes(v.blob.size), v.blob.contentType))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" fetching preview...")
	}
	return title + "\n" + v.vp.View()
}

func (v *previewView) Breadcrumb() string { return filepath.Base(v.blob.name) }

func (v *previewView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "j/k", Desc: "scroll"},
		{Keys: "s", Desc: "download blob"},
	}
}

// renderImage draws an image with "▀" half-blocks: each terminal cell holds
// two vertically stacked pixels (foreground = top, background = bottom),
// using truecolor. Works in any modern terminal — no graphics protocol.
func renderImage(data []byte, maxCols, maxRows int) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	if iw == 0 || ih == 0 {
		return "", fmt.Errorf("empty image")
	}

	pxW, pxH := maxCols, maxRows*2
	scale := minf(float64(pxW)/float64(iw), float64(pxH)/float64(ih))
	if scale > 1 {
		scale = 1
	}
	w := max(1, int(float64(iw)*scale))
	h := max(1, int(float64(ih)*scale))
	rows := (h + 1) / 2

	sample := func(x, y int) string {
		sx := bounds.Min.X + x*iw/w
		sy := bounds.Min.Y + y*ih/h
		r, g, b, _ := img.At(sx, sy).RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}

	var sb strings.Builder
	for row := 0; row < rows; row++ {
		for x := 0; x < w; x++ {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(sample(x, row*2)))
			if row*2+1 < h {
				style = style.Background(lipgloss.Color(sample(x, row*2+1)))
			}
			sb.WriteString(style.Render("▀"))
		}
		if row < rows-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
