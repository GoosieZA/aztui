package storage

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/ui"
)

type blobEntry struct {
	name        string // full blob name including prefix
	size        int64
	modified    string
	tier        string
	contentType string
}

type blobsLoadedMsg struct {
	dirs  []string
	blobs []blobEntry
	err   error
}

type downloadDoneMsg struct {
	path string
	size int64
	err  error
}

// blobsView browses one container at one prefix level; entering a virtual
// directory pushes another blobsView, so esc naturally walks back up.
type blobsView struct {
	client            *azblob.Client
	container, prefix string

	table   ui.Table
	spin    spinner.Model
	loading bool

	dirs  []string
	blobs []blobEntry

	width, height int
}

func newBlobsView(client *azblob.Client, containerName, prefix string) *blobsView {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = ui.TitleStyle
	t := ui.NewTable(
		ui.Column{Title: "NAME", Weight: 6},
		ui.Column{Title: "SIZE", Width: 8},
		ui.Column{Title: "MODIFIED", Width: 9},
		ui.Column{Title: "TIER", Width: 8},
	)
	t.Empty = "empty"
	return &blobsView{client: client, container: containerName, prefix: prefix, table: t, spin: sp, loading: true}
}

func (v *blobsView) Init() tea.Cmd {
	client, containerName, prefix := v.client, v.container, v.prefix
	return tea.Batch(v.spin.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		cc := client.ServiceClient().NewContainerClient(containerName)
		opts := &container.ListBlobsHierarchyOptions{MaxResults: to.Ptr[int32](5000)}
		if prefix != "" {
			opts.Prefix = &prefix
		}
		pager := cc.NewListBlobsHierarchyPager("/", opts)
		var dirs []string
		var blobs []blobEntry
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return blobsLoadedMsg{err: err}
			}
			if page.Segment == nil {
				continue
			}
			for _, p := range page.Segment.BlobPrefixes {
				dirs = append(dirs, deref(p.Name))
			}
			for _, b := range page.Segment.BlobItems {
				e := blobEntry{name: deref(b.Name)}
				if props := b.Properties; props != nil {
					if props.ContentLength != nil {
						e.size = *props.ContentLength
					}
					if props.LastModified != nil {
						e.modified = ui.Ago(*props.LastModified)
					}
					if props.AccessTier != nil {
						e.tier = string(*props.AccessTier)
					}
					e.contentType = deref(props.ContentType)
				}
				blobs = append(blobs, e)
			}
		}
		return blobsLoadedMsg{dirs: dirs, blobs: blobs}
	})
}

func (v *blobsView) setRows() {
	rows := make([][]string, 0, len(v.dirs)+len(v.blobs))
	for _, d := range v.dirs {
		rows = append(rows, []string{"📁 " + strings.TrimPrefix(d, v.prefix), "-", "-", "-"})
	}
	for _, b := range v.blobs {
		rows = append(rows, []string{
			strings.TrimPrefix(b.name, v.prefix),
			ui.Bytes(b.size),
			b.modified,
			b.tier,
		})
	}
	v.table.SetRows(rows)
}

// selected returns either a directory prefix or a blob entry.
func (v *blobsView) selected() (dir string, blob *blobEntry) {
	idx := v.table.CursorRow()
	if idx < 0 {
		return "", nil
	}
	if idx < len(v.dirs) {
		return v.dirs[idx], nil
	}
	idx -= len(v.dirs)
	if idx < len(v.blobs) {
		return "", &v.blobs[idx]
	}
	return "", nil
}

// downloadCmd saves a blob under ~/Downloads (or the working directory),
// uniquifying the file name rather than overwriting.
func downloadCmd(client *azblob.Client, containerName string, b blobEntry) tea.Cmd {
	return func() tea.Msg {
		opID := ui.BeginOp("downloading %s", path.Base(b.name))
		defer ui.EndOp(opID)
		dir, err := os.UserHomeDir()
		if err == nil {
			dl := filepath.Join(dir, "Downloads")
			if st, err := os.Stat(dl); err == nil && st.IsDir() {
				dir = dl
			}
		} else {
			dir = "."
		}
		dest := filepath.Join(dir, path.Base(b.name))
		base, ext := strings.TrimSuffix(dest, filepath.Ext(dest)), filepath.Ext(dest)
		for i := 1; ; i++ {
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				break
			}
			dest = fmt.Sprintf("%s-%d%s", base, i, ext)
		}

		f, err := os.Create(dest)
		if err != nil {
			return downloadDoneMsg{err: err}
		}
		defer f.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*opTimeout)
		defer cancel()
		n, err := client.DownloadFile(ctx, containerName, b.name, f, nil)
		if err != nil {
			os.Remove(dest)
			return downloadDoneMsg{err: err}
		}
		return downloadDoneMsg{path: dest, size: n}
	}
}

func (v *blobsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.table.SetSize(msg.Width, max(1, msg.Height-2))
		return v, nil

	case blobsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			return v, ui.Err(msg.err)
		}
		v.dirs, v.blobs = msg.dirs, msg.blobs
		v.setRows()
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
		if !v.table.InputActive() {
			switch msg.String() {
			case "enter":
				dir, blob := v.selected()
				if dir != "" {
					return v, ui.Push(newBlobsView(v.client, v.container, dir))
				}
				if blob != nil {
					return v, ui.Push(newPreviewView(v.client, v.container, *blob))
				}
				return v, nil
			case "s":
				if _, blob := v.selected(); blob != nil {
					return v, tea.Batch(ui.Status("downloading %s...", path.Base(blob.name)),
						downloadCmd(v.client, v.container, *blob))
				}
				return v, ui.Warnf("select a blob to download")
			case "R":
				v.loading = true
				return v, v.Init()
			}
		}
		return v, v.table.Update(msg)
	}
	return v, nil
}

func (v *blobsView) View() string {
	loc := v.container + "/" + v.prefix
	title := ui.TitleStyle.Render(" "+loc) +
		ui.DimStyle.Render(fmt.Sprintf("  %d dirs, %d blobs", len(v.dirs), len(v.blobs)))
	if v.loading {
		return title + "\n\n " + v.spin.View() + ui.DimStyle.Render(" listing blobs...")
	}
	return title + "\n" + v.table.View()
}

func (v *blobsView) InputActive() bool { return v.table.InputActive() }

func (v *blobsView) Breadcrumb() string {
	if v.prefix == "" {
		return v.container
	}
	return path.Base(strings.TrimSuffix(v.prefix, "/"))
}

func (v *blobsView) KeyHints() []ui.KeyHint {
	return []ui.KeyHint{
		{Keys: "enter", Desc: "open folder / preview blob"},
		{Keys: "s", Desc: "download blob to ~/Downloads"},
		{Keys: "R", Desc: "refresh"},
	}
}
