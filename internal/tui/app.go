// Package tui is the Bubble Tea front end: search input, live results list,
// and downloads dashboard.
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/control"
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
	eng    control.Engine
	agg    *aggregator.Aggregator
	st     *state.State
	health *health.Store

	screen screen
	width  int
	height int

	search     searchModel
	isos       isosModel
	results    resultsModel
	preview    previewModel
	downloads  downloadsModel
	compass    compassModel
	proxy      proxyBadge
	proxyCheck proxyChecker
	startup    tea.Cmd // optional one-shot action requested on the command line

	errText      string
	errGen       uint64
	toast        toastState
	showHelp     bool      // the `?` key card, drawn over whichever screen is active
	lastTickSave time.Time // throttles progress-only state.json writes on the tick
	searchSeq    uint64    // generation for streamed searches; rejects late messages
}

func New(cfg *config.Config, eng control.Engine, agg *aggregator.Aggregator, st *state.State, hs *health.Store) *App {
	a := &App{
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
	if runtime := cfg.ProxyRuntime(); runtime != nil && runtime.Enabled() {
		a.proxy.state = proxyBadgeUnverified
	}
	a.refreshDownloadItems()
	return a
}

// ShowDownloads opens the app on the downloads screen (used after autopilot
// queues torrents so the user lands straight on progress).
func (a *App) ShowDownloads() { a.screen = screenDownloads }

func (a *App) Init() tea.Cmd {
	// pick up torrents resumed from state.json at startup
	cmds := []tea.Cmd{a.search.input.Focus(), a.startDownloadPathCheck(true)}
	if snaps := a.eng.Snapshots(); len(snaps) > 0 {
		a.downloads.snaps = snaps
		a.downloads.ticking = true
		cmds = append(cmds, tickCmd(a.tickInterval()))
	}
	a.refreshDownloadItems()
	if proxyCmd := a.startProxyCheck(time.Now()); proxyCmd != nil {
		cmds = append(cmds, proxyCmd)
	}
	if a.startup != nil {
		cmds = append(cmds, a.startup)
		a.startup = nil
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
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		// Downloads is the one screen worth reaching mid-task - you queue
		// something, keep searching, and want to glance at progress without
		// walking the tab cycle. A chord rather than a letter is what makes
		// "from anywhere" true: it needs none of the guards a single key does,
		// so it still works while a query or a filter is being typed, where
		// every letter has to stay a letter. Preview stays inert, like tab: it
		// is a modal you leave with esc, not a stop on the cycle.
		if msg.String() == "ctrl+d" && a.screen != screenPreview {
			a.cancelResolve()
			a.showHelp = false
			a.screen = screenDownloads
			return a, a.startDownloadPathCheck(false)
		}
		if a.showHelp {
			// The card is a reference, not a mode: any key puts it away, so
			// there is no wrong guess at how to get out of it.
			a.showHelp = false
			return a, nil
		}
		switch msg.String() {
		case "?":
			if a.helpKeyAvailable() {
				a.showHelp = true
				return a, nil
			}
		case "tab":
			// tab always cycles screens (it means nothing inside a search box);
			// only the preview modal keeps it inert.
			if a.screen != screenPreview {
				return a, a.cycleScreen()
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
		a.refreshDownloadItems()
		proxyCmd := a.startProxyCheck(time.Time(msg))
		pathCmd := a.startDownloadPathCheck(false)
		if a.screen == screenPreview {
			a.preview.refresh(a.eng)
		}
		if a.tickShouldContinue() {
			return a, tea.Batch(saveCmd, proxyCmd, pathCmd, tickCmd(a.tickInterval()))
		}
		a.downloads.ticking = false
		return a, tea.Batch(saveCmd, proxyCmd, pathCmd)

	case torrentAddedMsg:
		return a, a.onTorrentAdded(msg)

	case previewReadyMsg:
		return a, a.onPreviewReady(msg)

	case resultMsg, resultsClosedMsg, statusMsg, statusClosedMsg, magnetResolvedMsg:
		// Search streams keep progressing even while the user checks downloads or
		// the ISO shelf. updateResults only changes the visible screen when a
		// selected action completes, so processing these globally is safe.
		return a.updateResults(msg)

	case clearErrMsg:
		if msg.gen == a.errGen {
			a.errText = ""
		}
		return a, nil

	case downloadPathsMsg:
		a.onDownloadPaths(msg)
		return a, nil

	case revealDownloadMsg:
		if msg.err != nil {
			return a, a.showError(msg.err.Error())
		}
		return a, nil

	case yankDoneMsg:
		if msg.err != nil {
			return a, a.showError(msg.err.Error())
		}
		return a, a.showToast("yanked "+msg.what, toastOK, toastQuick)

	case clearToastMsg:
		if msg.gen == a.toast.gen {
			a.toast.text = ""
		}
		return a, nil

	case healthDoneMsg:
		return a, a.onHealthDone(msg)

	case verifyDoneMsg:
		return a, a.onVerifyDone(msg)

	case proxyCheckMsg:
		a.onProxyCheck(msg)
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
	case screenHealth:
		return a.updateHealth(msg)
	default:
		return a.updateDownloads(msg)
	}
}

func (a *App) View() string {
	if a.showHelp {
		return a.viewHelp()
	}
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
		case engine.StateFetchingMeta, engine.StatePreviewing, engine.StateDownloading, engine.StateSeeding, engine.StateVerifying:
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

// tickInterval keeps the dashboard fluid without sampling every torrent four
// times a second. Two visual updates per second are ample for terminal progress
// bars; background screens sample more lazily and still persist every change.
func (a *App) tickInterval() time.Duration {
	if a.screen == screenDownloads || a.screen == screenPreview {
		return 500 * time.Millisecond
	}
	return 1500 * time.Millisecond
}

// onPreviewReady opens the preview screen once the metadata torrent is added.
func (a *App) onPreviewReady(msg previewReadyMsg) tea.Cmd {
	if msg.err != nil {
		return a.showError("preview failed: " + msg.err.Error())
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

func (a *App) cycleScreen() tea.Cmd {
	a.cancelResolve()
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
	if a.screen == screenDownloads {
		return a.startDownloadPathCheck(false)
	}
	return nil
}

// onTorrentAdded records the new download in state.json and confirms it with a
// toast, starting the stats tick if idle. It deliberately stays on the current
// screen: queuing used to jump to the downloads list, which made grabbing three
// things off one search a round trip through three screens. The header's
// activity chip is what keeps the new download visible from wherever you are.
func (a *App) onTorrentAdded(msg torrentAddedMsg) tea.Cmd {
	a.isos.resolving = false
	if msg.err != nil {
		return a.showError("add failed: " + msg.err.Error())
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
	a.downloads.snaps = a.eng.Snapshots()
	a.refreshDownloadItems()
	return tea.Batch(
		a.saveState(),
		a.ensureTick(),
		a.startProxyCheck(time.Now()),
		a.showToast(queuedToast(entry.Name), toastOK, toastQuick),
	)
}

// helpKeyAvailable keeps `?` inert while a text field owns the keyboard, with
// one exception: an empty home search box, because the front page is exactly
// where someone goes looking for the key list. Type anything and `?` is a
// literal question mark again.
func (a *App) helpKeyAvailable() bool {
	if a.screen == screenSearch {
		return strings.TrimSpace(a.search.input.Value()) == ""
	}
	return !a.typing()
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
		return a.showError("save failed: " + err.Error())
	}
	return nil
}

// showError replaces the footer error and returns a generation-aware timer.
// Without the generation, a timer belonging to an older error can erase a new
// failure almost immediately.
func (a *App) showError(text string) tea.Cmd {
	a.errText = text
	a.errGen++
	return clearErrCmd(a.errGen)
}
