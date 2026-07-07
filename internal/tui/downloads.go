package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/engine"
)

type downloadsModel struct {
	snaps   []engine.Snapshot
	cursor  int
	bar     progress.Model
	ticking bool

	confirmRemove *metainfo.Hash // pending removal awaiting y/d/n
}

func newDownloadsModel() downloadsModel {
	bar := progress.New(progress.WithDefaultGradient())
	bar.Width = 40
	bar.ShowPercentage = false
	return downloadsModel{bar: bar}
}

func (a *App) updateDownloads(msg tea.Msg) (tea.Model, tea.Cmd) {
	d := &a.downloads
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}

	if d.confirmRemove != nil {
		return a.updateRemoveConfirm(key)
	}

	switch key.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		a.screen = screenSearch
		return a, a.search.input.Focus()
	case "up", "k":
		d.cursor = max(0, d.cursor-1)
	case "down", "j":
		d.cursor = min(max(0, len(d.snaps)-1), d.cursor+1)
	case "s":
		if s, ok := d.selected(); ok {
			seedingNow := s.State == engine.StateSeeding
			a.eng.SetSeeding(s.Hash, !seedingNow)
			d.snaps = a.eng.Snapshots()
		}
	case "p":
		if s, ok := d.selected(); ok {
			if s.State == engine.StatePaused {
				if err := a.eng.Resume(s.Hash); err != nil {
					a.errText = "resume failed: " + err.Error()
					return a, clearErrCmd()
				}
				a.setPausedInState(s.Magnet, false)
			} else {
				a.eng.Pause(s.Hash)
				a.setPausedInState(s.Magnet, true)
			}
			d.snaps = a.eng.Snapshots()
		}
	case "x":
		if s, ok := d.selected(); ok {
			h := s.Hash
			d.confirmRemove = &h
		}
	}
	return a, nil
}

func (a *App) updateRemoveConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &a.downloads
	h := *d.confirmRemove
	d.confirmRemove = nil
	switch key.String() {
	case "y", "d":
		magnet := a.eng.Magnet(h)
		if err := a.eng.Remove(h, key.String() == "d"); err != nil {
			a.errText = "remove failed: " + err.Error()
			return a, clearErrCmd()
		}
		if magnet != "" {
			a.st.Remove(magnet)
			a.st.Save(a.cfg.StatePath())
		}
		d.snaps = a.eng.Snapshots()
		d.cursor = max(0, min(d.cursor, len(d.snaps)-1))
	}
	return a, nil
}

func (a *App) setPausedInState(magnet string, paused bool) {
	if e := a.st.Find(magnet); e != nil {
		e.Paused = paused
		a.st.Save(a.cfg.StatePath())
	}
}

func (d *downloadsModel) selected() (engine.Snapshot, bool) {
	if d.cursor < 0 || d.cursor >= len(d.snaps) {
		return engine.Snapshot{}, false
	}
	return d.snaps[d.cursor], true
}

func (a *App) viewDownloads() string {
	d := &a.downloads
	width := a.contentWidth()

	if len(d.snaps) == 0 {
		empty := lipgloss.JoinVertical(lipgloss.Center,
			styleDim.Render(strings.Join(sleepingCat, "\n")),
			"",
			styleFaint.Render("the cat's napping - nothing downloading"),
			"",
			styleDim.Render("press ")+styleKey.Render("tab")+styleDim.Render(" to go hunting"),
		)
		body := lipgloss.Place(width, a.bodyHeight(), lipgloss.Center, lipgloss.Center, empty)
		return a.chrome("downloads", body, hints(hint("tab", "search"), hint("q", "quit")))
	}

	// a content cat: all done → the cat caught everything
	ctx := "downloads"
	allDone := true
	for _, s := range d.snaps {
		if s.State != engine.StateSeeding && s.State != engine.StateDone {
			allDone = false
			break
		}
	}
	if allDone {
		ctx = "downloads · all caught  =^.^="
	}

	// keep bar width sensible within the column
	d.bar.Width = max(20, min(64, width-40))

	var b strings.Builder
	for i, s := range d.snaps {
		marker := "  "
		nameStyle := styleFg
		if i == d.cursor {
			marker = styleSelBar.Render("▍ ")
			nameStyle = lipgloss.NewStyle().Foreground(colBrand).Bold(true)
		}
		b.WriteString(marker + nameStyle.Render(truncate(s.Name, max(20, width-6))) + "\n")

		pct := fmt.Sprintf("%5.1f%%", s.Progress()*100)
		b.WriteString("  " + d.bar.ViewAs(s.Progress()) + "  " + styleDim.Render(pct) + "\n")

		stats := fmt.Sprintf("  %s / %s   %s   ETA %s   %s %d/%d   %s",
			humanBytes(s.BytesCompleted),
			humanBytes(s.Length),
			humanSpeed(s.SpeedBps),
			fmtETA(s.ETA),
			styleFaint.Render("peers"), s.PeersActive, s.PeersTotal,
			stateBadge(s.State),
		)
		b.WriteString(styleDim.Render(stats) + "\n\n")
	}

	help := hints(hint("↑↓", "move"), hint("s", "seed"), hint("p", "pause"), hint("x", "remove"), hint("esc", "search"), hint("q", "quit"))
	if d.confirmRemove != nil {
		help = styleErr.Render("remove?  ") + hints(hint("y", "keep files"), hint("d", "delete files"), hint("esc", "cancel"))
	}
	return a.chrome(ctx, b.String(), help)
}

// stateBadge renders a torrent state with a state-appropriate color. Seeding
// gets a gentle purr.
func stateBadge(s engine.TorrentState) string {
	switch s {
	case engine.StateSeeding:
		return styleOK.Render("seeding") + styleFaint.Render(" ~")
	case engine.StateDone:
		return styleOK.Render(s.String())
	case engine.StatePaused:
		return styleFaint.Render(s.String())
	case engine.StateFetchingMeta, engine.StatePreviewing:
		return styleDim.Render(s.String())
	}
	return styleStateTag.Render(s.String())
}
