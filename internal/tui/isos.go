package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/isos"
)

// isosModel is the Linux-ISO shelf shown on the home screen: a curated list of
// distributions whose official images tork can resolve and download over
// BitTorrent. It is browsed inline while the search field stays focused.
type isosModel struct {
	distros   []isos.Distro
	rows      []shelfRow // catalog flattened with category dividers
	win       listWindow // cursor over rows (skips dividers)
	resolving bool       // a download is being resolved
	active    string     // name of the distro currently being resolved
}

// shelfRow is one line in the shelf: a category divider (header != "") or a
// distro (distro >= 0, an index into isosModel.distros).
type shelfRow struct {
	header string
	distro int
}

func newISOsModel() isosModel {
	distros := isos.Catalog()
	m := isosModel{distros: distros, rows: buildShelfRows(distros)}
	m.win.cursor = m.nearestDistro(0, 1) // first selectable row
	return m
}

// buildShelfRows flattens the catalog into rows, inserting a divider whenever
// the category changes (the catalog is already grouped by category).
func buildShelfRows(distros []isos.Distro) []shelfRow {
	var rows []shelfRow
	last := ""
	for i, d := range distros {
		if cat := isos.CategoryOf(d); cat != last {
			rows = append(rows, shelfRow{header: cat, distro: -1})
			last = cat
		}
		rows = append(rows, shelfRow{distro: i})
	}
	return rows
}

// nearestDistro returns the first selectable row at or after `from` going in
// `dir` (+1/-1), or -1 if none.
func (m *isosModel) nearestDistro(from, dir int) int {
	for i := from; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].distro >= 0 {
			return i
		}
	}
	return -1
}

// selectRow moves the cursor by delta, skipping dividers (bouncing off edges).
func (m *isosModel) selectRow(delta, visible int) {
	total := len(m.rows)
	if total == 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	target := max(0, min(total-1, m.win.cursor+delta))
	sel := m.nearestDistro(target, dir)
	if sel < 0 {
		sel = m.nearestDistro(target, -dir)
	}
	if sel >= 0 {
		m.win.cursor = sel
		m.win.clamp(total, visible)
	}
}

// selected returns the distro under the cursor, if any.
func (m *isosModel) selected() (isos.Distro, bool) {
	if m.win.cursor < 0 || m.win.cursor >= len(m.rows) {
		return isos.Distro{}, false
	}
	if di := m.rows[m.win.cursor].distro; di >= 0 {
		return m.distros[di], true
	}
	return isos.Distro{}, false
}

// isoScreenRows is how many shelf rows fit on the dedicated ISOs screen
// (intro + blank above, "more" affordance below).
func (a *App) isoScreenRows() int {
	return max(3, a.bodyHeight()-3)
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
		m.selectRow(-1, rows)
	case "down", "j":
		m.selectRow(1, rows)
	case "pgup":
		m.selectRow(-rows, rows)
	case "pgdown":
		m.selectRow(rows, rows)
	case "g", "home":
		if s := m.nearestDistro(0, 1); s >= 0 {
			m.win.cursor = s
			m.win.clamp(len(m.rows), rows)
		}
	case "G", "end":
		if s := m.nearestDistro(len(m.rows)-1, -1); s >= 0 {
			m.win.cursor = s
			m.win.clamp(len(m.rows), rows)
		}
	case "enter":
		d, ok := m.selected()
		if m.resolving || !ok {
			return a, nil
		}
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

		t, err := isos.ResolveWithClient(ctx, d, a.cfg.ProxyHTTPClient())
		if err != nil {
			return torrentAddedMsg{name: d.Name, err: err}
		}
		if t.DirectURL != "" { // no torrent published: plain-https download
			h, aerr := a.eng.AddDirect(t.DirectURL, t.Title, t.SHA256)
			return torrentAddedMsg{hash: h, magnet: t.DirectURL, name: t.Title, sha256: t.SHA256, err: aerr}
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

// renderISOShelf draws the (scrolling) shelf for the ISOs screen: category
// dividers interleaved with distro rows, windowed and cursor-highlighted.
func (a *App) renderISOShelf(width, rows int) string {
	m := &a.isos
	lay := newISOsLayout(width)
	list := renderWindow(&m.win, len(m.rows), rows, width, func(i int, selected bool) string {
		row := m.rows[i]
		if row.distro < 0 {
			return styleFaint.Render("── ") + styleDim.Render(row.header)
		}
		d := m.distros[row.distro]
		return styleBrand.Render(padRight(d.Name, lay.nameW)) +
			styleFaint.Render(padRight(d.Edition, lay.edW)) +
			styleDim.Render(truncate(d.Blurb, lay.blurbW))
	})

	// a small "more below/above" affordance so scrolling is discoverable
	start, end := m.win.clamp(len(m.rows), rows)
	if hidden := len(m.rows) - (end - start); hidden > 0 {
		list += "\n" + styleFaint.Render(fmt.Sprintf("  … %d more (↑↓)", hidden))
	}
	return list
}
