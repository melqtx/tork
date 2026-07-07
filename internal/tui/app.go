// Package tui is the Bubble Tea front end: search input, live results list,
// and downloads dashboard.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

type screen int

const (
	screenSearch screen = iota
	screenISOs
	screenResults
	screenPreview
	screenDownloads
)

type App struct {
	cfg *config.Config
	eng *engine.Engine
	agg *aggregator.Aggregator
	st  *state.State

	screen screen
	width  int
	height int

	search    searchModel
	isos      isosModel
	results   resultsModel
	preview   previewModel
	downloads downloadsModel

	errText string
}

func New(cfg *config.Config, eng *engine.Engine, agg *aggregator.Aggregator, st *state.State) *App {
	return &App{
		cfg:       cfg,
		eng:       eng,
		agg:       agg,
		st:        st,
		search:    newSearchModel(),
		isos:      newISOsModel(),
		results:   newResultsModel(cfg.Ranking),
		downloads: newDownloadsModel(),
	}
}

// ShowDownloads opens the app on the downloads screen (used after autopilot
// queues torrents so the user lands straight on progress).
func (a *App) ShowDownloads() { a.screen = screenDownloads }

func (a *App) Init() tea.Cmd {
	// pick up torrents resumed from state.json at startup
	cmds := []tea.Cmd{a.search.input.Focus()}
	if snaps := a.eng.Snapshots(); len(snaps) > 0 {
		a.downloads.snaps = snaps
		a.downloads.ticking = true
		cmds = append(cmds, tickCmd())
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.downloads.bar.Width = min(60, max(20, a.width-50))
		return a, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		case "tab":
			// tab always cycles screens (it means nothing inside a search box);
			// only the preview modal keeps it inert.
			if a.screen != screenPreview {
				a.cycleScreen()
				return a, nil
			}
		}

	case tickMsg:
		a.downloads.snaps = a.eng.Snapshots()
		a.syncCompletedToState()
		if a.screen == screenPreview {
			a.preview.refresh(a.eng)
		}
		if a.tickShouldContinue() {
			return a, tickCmd()
		}
		a.downloads.ticking = false
		return a, nil

	case torrentAddedMsg:
		return a, a.onTorrentAdded(msg)

	case previewReadyMsg:
		return a, a.onPreviewReady(msg)

	case clearErrMsg:
		a.errText = ""
		return a, nil
	}

	switch a.screen {
	case screenSearch:
		return a.updateSearch(msg)
	case screenISOs:
		return a.updateISOs(msg)
	case screenResults:
		return a.updateResults(msg)
	case screenPreview:
		return a.updatePreview(msg)
	default:
		return a.updateDownloads(msg)
	}
}

func (a *App) View() string {
	switch a.screen {
	case screenSearch:
		return a.viewSearch()
	case screenISOs:
		return a.viewISOs()
	case screenResults:
		return a.viewResults()
	case screenPreview:
		return a.viewPreview()
	default:
		return a.viewDownloads()
	}
}

// tickShouldContinue keeps the stats tick armed while there is something to
// watch: active downloads or a preview awaiting metadata.
func (a *App) tickShouldContinue() bool {
	return len(a.downloads.snaps) > 0 || a.screen == screenPreview
}

// ensureTick starts the tick loop if it isn't already running.
func (a *App) ensureTick() tea.Cmd {
	if a.downloads.ticking {
		return nil
	}
	a.downloads.ticking = true
	return tickCmd()
}

// onPreviewReady opens the preview screen once the metadata torrent is added.
func (a *App) onPreviewReady(msg previewReadyMsg) tea.Cmd {
	if msg.err != nil {
		a.errText = "preview failed: " + msg.err.Error()
		return clearErrCmd()
	}
	a.preview = newPreviewModel(msg.hash, msg.magnet, msg.name)
	a.screen = screenPreview
	a.preview.refresh(a.eng) // metadata may already be cached
	return a.ensureTick()
}

// typing reports whether a text input currently owns the keyboard, so global
// single-letter shortcuts must stay inert.
func (a *App) typing() bool {
	return a.screen == screenSearch || (a.screen == screenResults && a.results.filtering)
}

func (a *App) cycleScreen() {
	switch a.screen {
	case screenSearch:
		a.screen = screenISOs
	case screenISOs:
		if len(a.results.rows) > 0 {
			a.screen = screenResults
		} else {
			a.screen = screenDownloads
		}
	case screenResults:
		a.screen = screenDownloads
	default:
		a.screen = screenSearch
	}
}

// onTorrentAdded records the new download in state.json and jumps to the
// downloads screen, starting the stats tick if idle.
func (a *App) onTorrentAdded(msg torrentAddedMsg) tea.Cmd {
	a.isos.resolving = false
	if msg.err != nil {
		a.errText = "add failed: " + msg.err.Error()
		return clearErrCmd()
	}
	a.st.Upsert(state.Entry{Magnet: msg.magnet, Name: msg.name, AddedAt: time.Now().UTC()})
	a.st.Save(a.cfg.StatePath())
	a.screen = screenDownloads
	a.downloads.snaps = a.eng.Snapshots()
	return a.ensureTick()
}

// syncCompletedToState marks finished torrents done in state.json (once).
func (a *App) syncCompletedToState() {
	dirty := false
	for _, s := range a.downloads.snaps {
		if s.State == engine.StateSeeding || s.State == engine.StateDone {
			if e := a.st.Find(s.Magnet); e != nil && !e.Done {
				e.Done = true
				if s.Name != "?" {
					e.Name = s.Name
				}
				dirty = true
			}
		}
	}
	if dirty {
		a.st.Save(a.cfg.StatePath())
	}
}
