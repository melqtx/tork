package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

// previewModel is the magnet sandbox: inspect a torrent's files and choose
// which to download before any data transfers.
type previewModel struct {
	hash     metainfo.Hash
	magnet   string
	name     string
	files    []engine.FileInfo
	ready    bool
	excluded map[int]bool // keyed by FileInfo.Index
	cursor   int
	offset   int
}

func newPreviewModel(h metainfo.Hash, magnet, name string) previewModel {
	return previewModel{hash: h, magnet: magnet, name: name, excluded: map[int]bool{}}
}

// refresh polls the engine for file metadata once it arrives.
func (p *previewModel) refresh(eng *engine.Engine) {
	if p.ready {
		return
	}
	if files, ok := eng.Files(p.hash); ok {
		sort.Slice(files, func(i, j int) bool { return files[i].Length > files[j].Length })
		p.files = files
		p.ready = true
	}
}

func (p *previewModel) selectedBytes() int64 {
	var sum int64
	for _, f := range p.files {
		if !p.excluded[f.Index] {
			sum += f.Length
		}
	}
	return sum
}

func (p *previewModel) totalBytes() int64 {
	var sum int64
	for _, f := range p.files {
		sum += f.Length
	}
	return sum
}

func (p *previewModel) excludedSlice() []int {
	var out []int
	for idx := range p.excluded {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

func (a *App) updatePreview(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	p := &a.preview
	switch key.String() {
	case "esc":
		a.eng.Remove(p.hash, false) // discard the metadata-only torrent
		a.screen = screenResults
		return a, nil
	}
	if !p.ready {
		return a, nil // ignore edits until the file list is known
	}
	switch key.String() {
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = max(0, min(len(p.files)-1, p.cursor+1))
	case "g", "home":
		p.cursor = 0
	case "G", "end":
		p.cursor = max(0, len(p.files)-1)
	case " ":
		if p.cursor >= 0 && p.cursor < len(p.files) {
			idx := p.files[p.cursor].Index
			if p.excluded[idx] {
				delete(p.excluded, idx)
			} else {
				p.excluded[idx] = true
			}
		}
	case "a": // include all
		p.excluded = map[int]bool{}
	case "n": // exclude all
		for _, f := range p.files {
			p.excluded[f.Index] = true
		}
	case "enter":
		if p.selectedBytes() == 0 {
			a.errText = "select at least one file"
			return a, clearErrCmd()
		}
		return a, a.startPreviewDownload()
	}
	return a, nil
}

// startPreviewDownload commits the file selection and jumps to downloads.
func (a *App) startPreviewDownload() tea.Cmd {
	p := &a.preview
	excluded := p.excludedSlice()
	a.eng.StartDownload(p.hash, excluded)
	a.st.Upsert(state.Entry{
		Magnet:   p.magnet,
		Name:     p.name,
		AddedAt:  time.Now().UTC(),
		Excluded: excluded,
	})
	a.st.Save(a.cfg.StatePath())
	a.screen = screenDownloads
	a.downloads.snaps = a.eng.Snapshots()
	return a.ensureTick()
}

func (a *App) viewPreview() string {
	p := &a.preview
	width := a.contentWidth()

	if !p.ready {
		msg := lipgloss.JoinVertical(lipgloss.Center,
			styleFg.Render(truncate(p.name, width-4)),
			"",
			styleDim.Render("fetching metadata…")+styleFaint.Render("  (needs peers; may take a moment)"),
		)
		body := lipgloss.Place(width, a.bodyHeight(), lipgloss.Center, lipgloss.Center, msg)
		return a.chrome("preview", body, hints(hint("esc", "cancel")))
	}

	var b strings.Builder
	b.WriteString(" " + styleFg.Render(truncate(p.name, max(20, width-40))) +
		styleFaint.Render("   total ") + styleDim.Render(humanBytes(p.totalBytes())) +
		styleFaint.Render(" · ") + styleOK.Render(humanBytes(p.selectedBytes())) + styleFaint.Render(" selected") + "\n\n")

	nameW := max(20, width-18)
	h := max(1, a.bodyHeight()-2) // header line + blank
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h {
		p.offset = p.cursor - h + 1
	}
	p.offset = max(0, min(p.offset, max(0, len(p.files)-h)))
	end := min(len(p.files), p.offset+h)

	for i := p.offset; i < end; i++ {
		f := p.files[i]
		box := styleOK.Render("◉")
		if p.excluded[f.Index] {
			box = styleFaint.Render("○")
		}
		line := fmt.Sprintf(" %s  %-*s %10s", box, nameW, truncate(f.Path, nameW), humanBytes(f.Length))
		switch {
		case i == p.cursor:
			line = styleSelected.Render(padRight(line, width))
		case p.excluded[f.Index]:
			line = styleFaint.Render(line)
		}
		b.WriteString(line + "\n")
	}
	for i := end - p.offset; i < h; i++ {
		b.WriteString("\n")
	}

	help := hints(hint("space", "toggle"), hint("a", "all"), hint("n", "none"), hint("enter", "download"), hint("esc", "cancel"))
	return a.chrome("preview", b.String(), help)
}
