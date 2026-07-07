package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/isos"
)

// isosModel is the Linux-ISO shelf shown on the home screen: a curated list of
// distributions whose official images tork can resolve and download over
// BitTorrent. It is browsed inline while the search field stays focused.
type isosModel struct {
	distros   []isos.Distro
	cursor    int
	offset    int    // scroll offset into distros
	resolving bool   // a download is being resolved
	active    string // name of the distro currently being resolved
}

func newISOsModel() isosModel {
	return isosModel{distros: isos.Catalog()}
}

// isoScreenRows is how many shelf rows fit on the dedicated ISOs screen.
func (a *App) isoScreenRows() int {
	return max(3, a.bodyHeight()-2)
}

func (a *App) updateISOs(msg tea.Msg) (tea.Model, tea.Cmd) {
	m := &a.isos
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	rows := a.isoScreenRows()
	switch key.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		a.screen = screenSearch
		return a, a.search.input.Focus()
	case "up", "k":
		m.move(-1, rows)
	case "down", "j":
		m.move(1, rows)
	case "pgup":
		m.move(-rows, rows)
	case "pgdown":
		m.move(rows, rows)
	case "g", "home":
		m.move(-len(m.distros), rows)
	case "G", "end":
		m.move(len(m.distros), rows)
	case "enter":
		if m.resolving || len(m.distros) == 0 {
			return a, nil
		}
		d := m.distros[m.cursor]
		m.resolving = true
		m.active = d.Name
		return a, a.downloadISOCmd(d)
	}
	return a, nil
}

func (a *App) viewISOs() string {
	m := &a.isos
	width := a.contentWidth()
	intro := styleDim.Render("official images, resolved live from each project’s own mirror") +
		styleFaint.Render(" · you seed it back when it’s done")
	body := intro + "\n\n" + a.renderISOShelf(width, a.isoScreenRows())

	var help string
	if m.resolving {
		help = styleDim.Render("fetching the latest " + m.active + " torrent…")
	} else {
		help = hints(hint("↑↓", "move"), hint("enter", "download"), hint("tab", "screens"), hint("esc", "home"))
	}
	return a.chrome("linux isos", body, help)
}

// move shifts the cursor by delta, keeping it inside a window of visible rows.
func (m *isosModel) move(delta, visible int) {
	if len(m.distros) == 0 {
		return
	}
	m.cursor = max(0, min(len(m.distros)-1, m.cursor+delta))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
}

// downloadISOCmd resolves the distro's current official torrent and hands it to
// the engine. It reuses torrentAddedMsg, so a resolved image flows into the
// same download/state path as any other add.
func (a *App) downloadISOCmd(d isos.Distro) tea.Cmd {
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return torrentAddedMsg{name: d.Name, err: fmt.Errorf("resolve panicked: %v", r)}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		t, err := isos.Resolve(ctx, d)
		if err != nil {
			return torrentAddedMsg{name: d.Name, err: err}
		}
		if t.Magnet != "" {
			h, aerr := a.eng.Add(t.Magnet, nil)
			return torrentAddedMsg{hash: h, magnet: t.Magnet, name: t.Title, err: aerr}
		}
		h, name, magnet, aerr := a.eng.AddTorrentURL(ctx, t.URL)
		if name == "" {
			name = t.Title
		}
		return torrentAddedMsg{hash: h, magnet: magnet, name: name, err: aerr}
	}
}

// renderISOShelf draws the (scrolling) distro list for the home screen, in a
// window of rows lines and highlighting the cursor.
func (a *App) renderISOShelf(width, rows int) string {
	m := &a.isos
	if rows < 1 {
		rows = 1
	}
	// keep the cursor within the visible window
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.distros)-rows)))
	end := min(len(m.distros), m.offset+rows)

	nameW, edW := 15, 26
	var b strings.Builder
	for i := m.offset; i < end; i++ {
		d := m.distros[i]
		tag := "  "
		if d.Server {
			tag = styleFaint.Render("⌂ ")
		}
		name := styleBrand.Render(padRight(d.Name, nameW))
		ed := styleFaint.Render(padRight(d.Edition, edW))
		line := " " + tag + name + ed + styleDim.Render(d.Blurb)
		if i == m.cursor {
			bar := styleSelBar.Render("▍")
			line = styleSelected.Render(padRight(bar+tag+name+ed+styleDim.Render(d.Blurb), width))
		}
		b.WriteString(line + "\n")
	}
	// a small "more below/above" affordance so scrolling is discoverable
	hidden := len(m.distros) - (end - m.offset)
	if hidden > 0 {
		b.WriteString(styleFaint.Render(fmt.Sprintf("  … %d more (↑↓)", hidden)))
	}
	return strings.TrimRight(b.String(), "\n")
}
