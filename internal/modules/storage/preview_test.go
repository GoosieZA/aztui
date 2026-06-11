package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Tests run without a TTY, where lipgloss would strip all color — force
// truecolor so the renderer's output can be asserted.
func init() { lipgloss.SetColorProfile(termenv.TrueColor) }

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding test png: %v", err)
	}
	return buf.Bytes()
}

func TestRenderImageFitsBounds(t *testing.T) {
	out, err := renderImage(testPNG(t, 120, 60), 40, 12)
	if err != nil {
		t.Fatalf("renderImage: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) > 12 {
		t.Errorf("rendered %d rows, want <= 12", len(lines))
	}
	if !strings.Contains(out, "▀") {
		t.Error("expected half-block cells in output")
	}
	if !strings.Contains(out, "38;2;") && !strings.Contains(out, "38;5;") {
		t.Error("expected color escape sequences in output")
	}
}

func TestRenderImageTinyAndUpscaleCap(t *testing.T) {
	// A 2x2 image must not be blown up past its native size.
	out, err := renderImage(testPNG(t, 2, 2), 80, 40)
	if err != nil {
		t.Fatalf("renderImage: %v", err)
	}
	if rows := len(strings.Split(out, "\n")); rows != 1 {
		t.Errorf("2px-tall image should render one half-block row, got %d", rows)
	}
}

func TestRenderImageRejectsGarbage(t *testing.T) {
	if _, err := renderImage([]byte("not an image"), 20, 10); err == nil {
		t.Error("expected decode error for garbage input")
	}
}

func TestPreviewKind(t *testing.T) {
	cases := []struct{ name, ct, want string }{
		{"photo.jpg", "", "image"},
		{"diagram.png", "application/octet-stream", "image"},
		{"data.bin", "image/webp", "image"},
		{"config.json", "", "text"},
		{"notes", "text/plain", "text"},
		{"app.log", "", "text"},
		{"archive.zip", "application/zip", "unknown"},
	}
	for _, c := range cases {
		if got := previewKind(c.name, c.ct); got != c.want {
			t.Errorf("previewKind(%q, %q) = %q, want %q", c.name, c.ct, got, c.want)
		}
	}
}
