// Package tui is the Bubble Tea front end: search input, live results list,
// and downloads dashboard.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/state"
)

type screen int

const (
	screenSearch screen = iota
	screenISOs
	screenResults
	screenPreview
	screenDownloads
	screenHealth
)

type App struct {
	cfg    *config.Config
	eng    *engine.Engine
	agg    *aggregator.Aggregator
	st     *state.State
	health *health.Store

	screen screen
	width  int
	height int

	search    searchModel
	isos      isosModel
	results   resultsModel
	preview   previewModel
	downloads downloadsModel
	compass   compassModel

	errText      string
	lastTickSave time.Time // throttles progress-only state.json writes on the tick
}

func New(cfg *config.Config, eng *engine.Engine, agg *aggregator.Aggregator, st *state.State, hs *health.Store) *App {
	return &App{
		cfg:       cfg,
		eng:       eng,
		agg:       agg,
		st:        st,
		health:    hs,
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
		cmds = append(cmds, tickCmd(a.tickInterval()))
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// viewDownloads re-derives the bar width from contentWidth every render
		a.width, a.height = msg.Width, msg.Height
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
		case "H":
			// The health screen is reachable from anywhere a capital letter is
			// not being typed, and is deliberately outside the tab cycle.
			if a.health != nil && !a.typing() && a.screen != screenPreview && a.screen != screenHealth {
				return a, a.openHealth()
			}
		}

	case tickMsg:
		a.downloads.snaps = a.eng.Snapshots()
		saveCmd := a.syncCompletedToState()
		if a.screen == screenPreview {
			a.preview.refresh(a.eng)
		}
		if a.tickShouldContinue() {
			return a, tea.Batch(saveCmd, tickCmd(a.tickInterval()))
		}
		a.downloads.ticking = false
		return a, saveCmd

	case torrentAddedMsg:
		return a, a.onTorrentAdded(msg)

	case previewReadyMsg:
		return a, a.onPreviewReady(msg)

	case clearErrMsg:
		a.errText = ""
		return a, nil

	case healthDoneMsg:
		return a, a.onHealthDone(msg)
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
	case screenHealth:
		return a.updateHealth(msg)
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
	case screenHealth:
		return a.viewHealth()
	default:
		return a.viewDownloads()
	}
}

// tickShouldContinue keeps the stats tick armed while there is something to
// watch: active downloads or a preview awaiting metadata.
func (a *App) tickShouldContinue() bool {
	if a.screen == screenPreview {
		return true
	}
	for _, s := range a.downloads.snaps {
		switch s.State {
		case engine.StateFetchingMeta, engine.StatePreviewing, engine.StateDownloading, engine.StateSeeding:
			return true
		}
	}
	return false
}

// ensureTick starts the tick loop if it isn't already running.
func (a *App) ensureTick() tea.Cmd {
	if a.downloads.ticking {
		return nil
	}
	a.downloads.ticking = true
	return tickCmd(a.tickInterval())
}

// tickInterval polls fast enough to feel fluid on the screens that show live
// progress, and lazily elsewhere (background downloads still get saved).
func (a *App) tickInterval() time.Duration {
	if a.screen == screenDownloads || a.screen == screenPreview {
		return 250 * time.Millisecond
	}
	return time.Second
}

// onPreviewReady opens the preview screen once the metadata torrent is added.
func (a *App) onPreviewReady(msg previewReadyMsg) tea.Cmd {
	if msg.err != nil {
		a.errText = "preview failed: " + msg.err.Error()
		return clearErrCmd()
	}
	a.preview = newPreviewModel(msg.hash, msg.magnet, msg.name, msg.from, msg.owned)
	a.screen = screenPreview
	a.preview.refresh(a.eng) // metadata may already be cached
	return a.ensureTick()
}

// typing reports whether a text input currently owns the keyboard, so global
// single-letter shortcuts must stay inert.
func (a *App) typing() bool {
	return a.screen == screenSearch ||
		(a.screen == screenResults && a.results.filtering) ||
		a.downloads.prompt.action != pathActionNone
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
	entry := state.Entry{
		Magnet:      msg.magnet,
		Name:        msg.name,
		SHA256:      msg.sha256,
		AddedAt:     time.Now().UTC(),
		DownloadDir: a.cfg.DownloadDir,
		Seed:        state.Bool(a.cfg.SeedAfterComplete),
	}
	if snap, ok := a.eng.Snapshot(msg.hash); ok {
		applySnapshotToEntry(&entry, snap)
	}
	a.st.Upsert(entry)
	a.screen = screenDownloads
	a.downloads.snaps = a.eng.Snapshots()
	return tea.Batch(a.saveState(), a.ensureTick())
}

// syncCompletedToState marks finished torrents done in state.json and keeps
// progress bookkeeping current. Transitions (name/path/seed/done) are persisted
// immediately; progress-only churn (bytes ticking up) is written at most once
// every 5s so a busy download doesn't hammer the disk from inside Update.
func (a *App) syncCompletedToState() tea.Cmd {
	saveNow, progressed := false, false
	for _, s := range a.downloads.snaps {
		if e := a.st.Find(s.Magnet); e != nil {
			meta, prog := applySnapshotToEntry(e, s)
			saveNow = saveNow || meta
			progressed = progressed || prog
		}
		if s.State == engine.StateSeeding || s.State == engine.StateDone {
			if e := a.st.Find(s.Magnet); e != nil && !e.Done {
				e.Done = true
				now := time.Now().UTC()
				e.CompletedAt = &now
				saveNow = true
			}
		}
	}
	switch {
	case saveNow:
	case progressed && time.Since(a.lastTickSave) >= 5*time.Second:
	default:
		return nil
	}
	a.lastTickSave = time.Now()
	return a.saveState()
}

// applySnapshotToEntry copies live snapshot fields onto a state entry, reporting
// whether a meta field (name/dir/path/seed) or only progress bytes changed.
func applySnapshotToEntry(e *state.Entry, s engine.Snapshot) (meta, progress bool) {
	if s.Name != "" && s.Name != "?" && e.Name != s.Name {
		e.Name = s.Name
		meta = true
	}
	if s.DownloadDir != "" && e.DownloadDir != s.DownloadDir {
		e.DownloadDir = s.DownloadDir
		meta = true
	}
	if s.DataPath != "" && e.DataPath != s.DataPath {
		e.DataPath = s.DataPath
		meta = true
	}
	if e.NeedsRelink && s.DataPath != "" {
		e.NeedsRelink = false
		meta = true
	}
	if e.Seed == nil || *e.Seed != s.Seed {
		e.Seed = state.Bool(s.Seed)
		meta = true
	}
	if e.BytesCompleted != s.BytesCompleted {
		e.BytesCompleted = s.BytesCompleted
		progress = true
	}
	if e.Length != s.Length {
		e.Length = s.Length
		progress = true
	}
	return meta, progress
}

func (a *App) saveState() tea.Cmd {
	if err := a.st.Save(a.cfg.StatePath()); err != nil {
		a.errText = "save failed: " + err.Error()
		return clearErrCmd()
	}
	return nil
}
