package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

// Desktop handoff. "reveal" shows a download in the platform's file manager;
// "open" hands it to whatever normally opens that file type, which is the
// natural last step of a finished download and saves a trip to a file manager.
var revealAvailable = runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows"

// revealLabel names the action in the platform's own words.
var revealLabel = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "reveal in Finder"
	case "windows":
		return "show in Explorer"
	default:
		return "open the containing folder"
	}
}()

func revealCommand(path string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{"-R", path}
	case "windows":
		return "explorer", []string{"/select," + path}
	default:
		return "xdg-open", []string{filepath.Dir(path)}
	}
}

func openCommand(path string) (string, []string) {
	switch runtime.GOOS {
	case "windows":
		return "explorer", []string{path}
	case "darwin":
		return "open", []string{path}
	default:
		return "xdg-open", []string{path}
	}
}

type downloadItem struct {
	Hash           metainfo.Hash
	Magnet         string
	Name           string
	DownloadDir    string
	DataPath       string
	BytesCompleted int64
	Length         int64
	SpeedBps       float64
	ETA            time.Duration
	PeersActive    int
	PeersTotal     int
	Seeders        int
	Trackers       int
	MetadataSource engine.MetadataSource
	DHTEnabled     bool
	ProxyStrict    bool
	State          engine.TorrentState
	Note           string
	Seed           bool
	Live           bool
	EntryIndex     int
}

// downloadMetrics is computed once with the cached list projection, then used
// by the header, overview and detail panel without rescanning on every View.
type downloadMetrics struct {
	Total, Active, Paused, Done, Missing, Seeding, Verifying int
	BytesCompleted, Length, ActiveRemaining                  int64
	SpeedBps                                                 float64
	PeersActive, PeersTotal, Seeders                         int
}

func (m downloadMetrics) Progress() float64 {
	if m.Length <= 0 {
		return 0
	}
	return clampProgress(float64(m.BytesCompleted) / float64(m.Length))
}

func (m downloadMetrics) ETA() time.Duration {
	if m.SpeedBps <= 0 || m.ActiveRemaining <= 0 {
		return 0
	}
	return time.Duration(float64(m.ActiveRemaining) / m.SpeedBps * float64(time.Second))
}

type removeConfirm struct {
	item       downloadItem
	deleteData bool
}

type revealDownloadMsg struct{ err error }

// yankDoneMsg reports a clipboard copy; what names the copied field ("path",
// "magnet") so the popup can say which one landed.
type yankDoneMsg struct {
	what string
	err  error
}

type pathAction int

const (
	pathActionNone pathAction = iota
	pathActionMove
	pathActionRelink
)

type pathPrompt struct {
	action pathAction
	magnet string
	input  textinput.Model
}

type downloadsModel struct {
	snaps           []engine.Snapshot
	items           []downloadItem
	metrics         downloadMetrics
	itemsReady      bool
	selectedKey     string
	selectionPinned bool // false follows the most actionable item until the user moves
	win             listWindow
	ticking         bool
	pathExists      map[string]bool
	pathCheckID     uint64
	checkingPaths   bool
	pathsChecked    time.Time

	confirmRemove *removeConfirm
	prompt        pathPrompt
}

func newDownloadsModel() downloadsModel {
	return downloadsModel{pathExists: map[string]bool{}}
}

const downloadPathCheckInterval = 30 * time.Second

// startDownloadPathCheck snapshots the paths on the update goroutine and does
// the potentially slow filesystem work in a command. This matters for network
// mounts in particular: View can run many times per second and must be pure.
func (a *App) startDownloadPathCheck(force bool) tea.Cmd {
	d := &a.downloads
	if d.checkingPaths || a.st == nil {
		return nil
	}
	if !force && !d.pathsChecked.IsZero() && time.Since(d.pathsChecked) < downloadPathCheckInterval {
		return nil
	}
	paths := make([]string, 0, len(a.st.Entries))
	seen := make(map[string]bool, len(a.st.Entries))
	for _, entry := range a.st.Entries {
		path := strings.TrimSpace(entry.DataPath)
		if !entry.Done || entry.NeedsRelink || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	d.pathCheckID++
	checkID := d.pathCheckID
	d.checkingPaths = true
	return func() tea.Msg {
		exists := make(map[string]bool, len(paths))
		for _, path := range paths {
			_, err := os.Stat(path)
			exists[path] = err == nil
		}
		return downloadPathsMsg{checkID: checkID, exists: exists}
	}
}

func (a *App) onDownloadPaths(msg downloadPathsMsg) {
	d := &a.downloads
	if msg.checkID != d.pathCheckID {
		return
	}
	d.pathExists = msg.exists
	d.checkingPaths = false
	d.pathsChecked = time.Now()
	a.refreshDownloadItems()
}

func (a *App) updateDownloads(msg tea.Msg) (tea.Model, tea.Cmd) {
	d := &a.downloads
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if d.prompt.action != pathActionNone {
			var cmd tea.Cmd
			d.prompt.input, cmd = d.prompt.input.Update(msg)
			return a, cmd
		}
		return a, nil
	}

	if d.prompt.action != pathActionNone {
		return a.updatePathPrompt(key)
	}
	if d.confirmRemove != nil {
		return a.updateRemoveConfirm(key)
	}

	items := a.downloadItems()
	rows := a.downloadListRows()
	if verificationBlocksKey(key.String()) {
		if it, ok := a.selectedDownload(items); ok && it.State == engine.StateVerifying {
			return a, a.showError("verification in progress")
		}
	}
	switch key.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		return a, a.navigate(a.downloadsFrom)
	case "up", "k":
		d.selectionPinned = true
		d.win.move(-1, len(items), rows)
	case "down", "j":
		d.selectionPinned = true
		d.win.move(1, len(items), rows)
	case "pgup":
		d.selectionPinned = true
		d.win.move(-rows, len(items), rows)
	case "pgdown":
		d.selectionPinned = true
		d.win.move(rows, len(items), rows)
	case "g", "home":
		d.selectionPinned = true
		d.win.home()
	case "G", "end":
		d.selectionPinned = true
		d.win.end(len(items), rows)
	case "s":
		if it, ok := a.selectedDownload(items); ok {
			return a, a.toggleSeed(it)
		}
	case "p":
		if it, ok := a.selectedDownload(items); ok {
			return a, a.togglePause(it)
		}
	case "v":
		if it, ok := a.selectedDownload(items); ok {
			return a, a.verifyDownload(it)
		}
	case "m":
		if it, ok := a.selectedDownload(items); ok {
			d.prompt = newPathPrompt(pathActionMove, it, "new folder: ", it.DownloadDir)
			return a, d.prompt.input.Focus()
		}
	case "r":
		if it, ok := a.selectedDownload(items); ok {
			d.prompt = newPathPrompt(pathActionRelink, it, "existing path: ", it.DataPath)
			return a, d.prompt.input.Focus()
		}
	case "y":
		if it, ok := a.selectedDownload(items); ok {
			return a, yankDownloadValue("path", it.DataPath)
		}
	case "Y":
		if it, ok := a.selectedDownload(items); ok {
			return a, yankDownloadValue("magnet", it.Magnet)
		}
	case "x":
		if it, ok := a.selectedDownload(items); ok {
			d.confirmRemove = &removeConfirm{item: it}
		}
	case "d":
		if it, ok := a.selectedDownload(items); ok {
			d.confirmRemove = &removeConfirm{item: it, deleteData: true}
		}
	case "enter":
		if it, ok := a.selectedDownload(items); ok {
			return a, openDownload(it)
		}
	case "o":
		if revealAvailable {
			it, ok := a.selectedDownload(items)
			if !ok {
				break
			}
			return a, revealDownload(it)
		}
	}
	d.syncSelection(items)
	return a, nil
}

func verificationBlocksKey(key string) bool {
	switch key {
	case "p", "s", "v", "m", "r", "x", "d":
		return true
	default:
		return false
	}
}

func yankDownloadValue(what, value string) tea.Cmd {
	return func() tea.Msg {
		text := strings.TrimSpace(value)
		if text == "" {
			return yankDoneMsg{what: what, err: fmt.Errorf("copy needs a known %s", what)}
		}
		if err := writeClipboard(text); err != nil {
			return yankDoneMsg{what: what, err: fmt.Errorf("copy %s failed: %w", what, err)}
		}
		return yankDoneMsg{what: what}
	}
}

// writeClipboard prefers the OS clipboard: bubbletea's renderer writes frames
// to stdout from its own goroutine, so emitting OSC 52 here can interleave
// with a frame mid-sequence. The escape-sequence path survives only as a
// fallback for hosts without a clipboard helper (e.g. a bare SSH session).
func writeClipboard(text string) error {
	if err := clipboard.WriteAll(text); err == nil {
		return nil
	}
	_, err := clipboardSequence(text).WriteTo(os.Stdout)
	return err
}

func clipboardSequence(text string) osc52.Sequence {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		return seq.Tmux()
	case os.Getenv("STY") != "":
		return seq.Screen()
	default:
		return seq
	}
}

func revealDownload(it downloadItem) tea.Cmd {
	return desktopHandoff("reveal", it.DataPath, false, revealCommand)
}

func openDownload(it downloadItem) tea.Cmd {
	return desktopHandoff("open", it.DataPath, true, openCommand)
}

// desktopHandoff shells out to the platform's file manager or opener.
// mustExist guards the open case: handing the desktop a path that is not on
// disk yet pops an error dialog outside the terminal, which is a confusing way
// to find out a download has not finished.
func desktopHandoff(verb, path string, mustExist bool, command func(string) (string, []string)) tea.Cmd {
	return func() tea.Msg {
		path = strings.TrimSpace(path)
		if path == "" {
			return revealDownloadMsg{err: fmt.Errorf("%s needs a known saved path", verb)}
		}
		if mustExist {
			if _, err := os.Stat(path); err != nil {
				return revealDownloadMsg{err: fmt.Errorf("nothing to open yet at %s", filepath.Base(path))}
			}
		}
		name, args := command(path)
		if err := exec.Command(name, args...).Run(); err != nil {
			return revealDownloadMsg{err: fmt.Errorf("%s failed: %w", verb, err)}
		}
		return revealDownloadMsg{}
	}
}

// overlayBottomRight splices toast into base's bottom-right corner, keeping
// whatever styled text sits to its left on those rows.
func overlayBottomRight(base, toast string, width int) string {
	baseLines := strings.Split(base, "\n")
	toastLines := strings.Split(toast, "\n")
	top := len(baseLines) - len(toastLines)
	if top < 0 {
		return base
	}
	for i, tl := range toastLines {
		idx := top + i
		keep := max(0, width-lipgloss.Width(tl)-1)
		left := ansi.Truncate(baseLines[idx], keep, "")
		baseLines[idx] = left + strings.Repeat(" ", max(1, keep-lipgloss.Width(left)+1)) + tl
	}
	return strings.Join(baseLines, "\n")
}

func newPathPrompt(action pathAction, it downloadItem, prompt, value string) pathPrompt {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.CharLimit = 512
	ti.Width = 72
	ti.PromptStyle = styleBrand
	ti.TextStyle = styleFg
	ti.PlaceholderStyle = styleFaint
	ti.Cursor.Style = styleBrand
	if value != "" {
		ti.SetValue(value)
	}
	return pathPrompt{action: action, magnet: it.Magnet, input: ti}
}

func (a *App) updatePathPrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &a.downloads
	switch key.String() {
	case "esc":
		d.prompt = pathPrompt{}
		return a, nil
	case "enter":
		target := strings.TrimSpace(d.prompt.input.Value())
		action := d.prompt.action
		magnet := d.prompt.magnet
		d.prompt = pathPrompt{}
		it, ok := a.downloadItemByMagnet(a.downloadItems(), magnet)
		if !ok {
			return a, a.showError("download is no longer in the list")
		}
		var err error
		if action == pathActionMove {
			err = a.moveDownload(it, target)
		} else {
			err = a.relinkDownload(it, target)
		}
		if err != nil {
			return a, a.showError(err.Error())
		}
		return a, tea.Batch(a.saveState(), a.ensureTick())
	}
	var cmd tea.Cmd
	d.prompt.input, cmd = d.prompt.input.Update(key)
	return a, cmd
}

func (a *App) updateRemoveConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &a.downloads
	conf := d.confirmRemove
	switch key.String() {
	case "esc", "n":
		d.confirmRemove = nil
		return a, nil
	case "y", "enter":
		d.confirmRemove = nil
		if err := a.removeDownload(conf.item, conf.deleteData); err != nil {
			return a, a.showError("remove failed: " + err.Error())
		}
		d.snaps = a.eng.Snapshots()
		a.refreshDownloadItems()
		return a, a.saveState()
	}
	return a, nil
}

func (a *App) selectedDownload(items []downloadItem) (downloadItem, bool) {
	if a.downloads.win.cursor < 0 || a.downloads.win.cursor >= len(items) {
		return downloadItem{}, false
	}
	return items[a.downloads.win.cursor], true
}

func (a *App) downloadItemByMagnet(items []downloadItem, magnet string) (downloadItem, bool) {
	for _, it := range items {
		if it.Magnet == magnet {
			return it, true
		}
	}
	return downloadItem{}, false
}

func (a *App) downloadItems() []downloadItem {
	if a.downloads.itemsReady {
		return a.downloads.items
	}
	return a.buildDownloadItems()
}

// refreshDownloadItems builds the state+engine projection once per update,
// sorts actionable transfers above history, computes dashboard totals, and
// restores the cursor by identity if an item's state moved it in the queue.
func (a *App) refreshDownloadItems() {
	d := &a.downloads
	selected := d.selectedKey
	if selected == "" && d.win.cursor >= 0 && d.win.cursor < len(d.items) {
		selected = downloadItemKey(d.items[d.win.cursor])
	}
	items := a.buildDownloadItems()
	sort.SliceStable(items, func(i, j int) bool {
		return downloadStatePriority(items[i].State) < downloadStatePriority(items[j].State)
	})
	d.items = items
	d.metrics = summarizeDownloads(items)
	d.itemsReady = true

	if d.selectionPinned && selected != "" {
		for i := range items {
			if downloadItemKey(items[i]) == selected {
				d.win.cursor = i
				break
			}
		}
	} else if !d.selectionPinned {
		d.win.home()
	}
	d.win.clamp(len(items), a.downloadListRows())
	d.syncSelection(items)
}

func (a *App) buildDownloadItems() []downloadItem {
	snapsByMagnet := make(map[string]engine.Snapshot, len(a.downloads.snaps))
	for _, s := range a.downloads.snaps {
		if s.Magnet != "" {
			snapsByMagnet[s.Magnet] = s
		}
	}
	if a.st == nil {
		out := make([]downloadItem, 0, len(a.downloads.snaps))
		for _, s := range a.downloads.snaps {
			out = append(out, itemFromSnapshot(s, -1, true))
		}
		return out
	}

	used := make(map[string]bool, len(snapsByMagnet))
	out := make([]downloadItem, 0, len(a.st.Entries)+len(a.downloads.snaps))
	for i := range a.st.Entries {
		e := &a.st.Entries[i]
		if s, ok := snapsByMagnet[e.Magnet]; ok {
			out = append(out, itemFromSnapshot(s, i, true))
			used[e.Magnet] = true
			continue
		}
		out = append(out, a.itemFromEntry(e, i))
	}
	for _, s := range a.downloads.snaps {
		if !used[s.Magnet] {
			out = append(out, itemFromSnapshot(s, -1, true))
		}
	}
	return out
}

func downloadItemKey(it downloadItem) string {
	if it.Magnet != "" {
		return "magnet:" + it.Magnet
	}
	if it.Hash != (metainfo.Hash{}) {
		return "hash:" + it.Hash.HexString()
	}
	return "path:" + it.DataPath + "\x00" + it.Name
}

func (d *downloadsModel) syncSelection(items []downloadItem) {
	if d.win.cursor < 0 || d.win.cursor >= len(items) {
		d.selectedKey = ""
		return
	}
	d.selectedKey = downloadItemKey(items[d.win.cursor])
}

func downloadStatePriority(s engine.TorrentState) int {
	switch s {
	case engine.StateDownloading, engine.StateFetchingMeta, engine.StateVerifying, engine.StatePreviewing:
		return 0
	case engine.StatePaused:
		return 1
	case engine.StateMissing:
		return 2
	case engine.StateSeeding:
		return 3
	case engine.StateDone:
		return 4
	default:
		return 2
	}
}

func summarizeDownloads(items []downloadItem) downloadMetrics {
	var m downloadMetrics
	m.Total = len(items)
	for _, it := range items {
		if it.Length > 0 {
			m.Length += it.Length
			completed := min(it.BytesCompleted, it.Length)
			if it.State == engine.StateDone || it.State == engine.StateSeeding {
				completed = it.Length
			}
			m.BytesCompleted += max(int64(0), completed)
		}
		m.SpeedBps += max(0, it.SpeedBps)
		m.PeersActive += it.PeersActive
		m.PeersTotal += it.PeersTotal
		m.Seeders += it.Seeders
		switch it.State {
		case engine.StateDownloading, engine.StateFetchingMeta, engine.StatePreviewing:
			m.Active++
			m.ActiveRemaining += max(int64(0), it.Length-it.BytesCompleted)
		case engine.StateVerifying:
			m.Active++
			m.Verifying++
		case engine.StatePaused:
			m.Paused++
		case engine.StateMissing:
			m.Missing++
		case engine.StateSeeding:
			m.Seeding++
			m.Done++
		case engine.StateDone:
			m.Done++
		}
	}
	return m
}

func itemFromSnapshot(s engine.Snapshot, idx int, live bool) downloadItem {
	return downloadItem{
		Hash: s.Hash, Magnet: s.Magnet, Name: s.Name,
		DownloadDir: s.DownloadDir, DataPath: s.DataPath,
		BytesCompleted: s.BytesCompleted, Length: s.Length,
		SpeedBps: s.SpeedBps, ETA: s.ETA,
		PeersActive: s.PeersActive, PeersTotal: s.PeersTotal, Seeders: s.Seeders,
		Trackers: s.Metadata.Trackers, MetadataSource: s.Metadata.Source,
		DHTEnabled: s.Metadata.DHTEnabled, ProxyStrict: s.Metadata.ProxyStrict,
		State: s.State, Note: s.Note, Seed: s.Seed,
		Live: live, EntryIndex: idx,
	}
}

func (a *App) itemFromEntry(e *state.Entry, idx int) downloadItem {
	name := e.Name
	if name == "" {
		name = "unknown download"
	}
	defaultSeed := false
	if a.cfg != nil {
		defaultSeed = a.cfg.SeedAfterComplete
	}
	st := engine.StatePaused
	note := "not active"
	path := strings.TrimSpace(e.DataPath)
	pathExists, pathKnown := a.downloads.pathExists[path]
	switch {
	case e.NeedsRelink:
		st = engine.StateMissing
		note = "save path unknown - relink before retrying"
	case e.Done && path == "":
		st = engine.StateMissing
		note = "save path unknown - relink before retrying"
	case e.Done && pathKnown && !pathExists:
		st = engine.StateMissing
		note = "files not found - relink or move before retrying"
	case e.Done:
		st = engine.StateDone
		note = "saved in state"
		if !pathKnown {
			note += " - checking files"
		}
	case e.Paused:
		st = engine.StatePaused
		note = "paused"
	}
	return downloadItem{
		Magnet: e.Magnet, Name: name, DownloadDir: e.DownloadDir, DataPath: e.DataPath,
		BytesCompleted: e.BytesCompleted, Length: e.Length,
		State: st, Note: note, Seed: e.SeedEnabled(defaultSeed),
		EntryIndex: idx,
	}
}

func (a *App) toggleSeed(it downloadItem) tea.Cmd {
	if strings.HasPrefix(it.Magnet, "http://") || strings.HasPrefix(it.Magnet, "https://") {
		return a.showError("direct downloads cannot seed")
	}
	next := !it.Seed
	if it.Live {
		if err := a.eng.SetSeeding(it.Hash, next); err != nil {
			return a.showError("seed change failed: " + err.Error())
		}
		a.downloads.snaps = a.eng.Snapshots()
	}
	var save tea.Cmd
	if e := a.st.Find(it.Magnet); e != nil {
		e.Seed = state.Bool(next)
		save = a.saveState()
	}
	a.refreshDownloadItems()
	return save
}

func (a *App) togglePause(it downloadItem) tea.Cmd {
	if it.State == engine.StateMissing {
		return a.showError("missing data - press r to relink, or d to delete it")
	}
	if it.Live && it.State != engine.StatePaused {
		if err := a.eng.Pause(it.Hash); err != nil {
			return a.showError("pause failed: " + err.Error())
		}
		if e := a.st.Find(it.Magnet); e != nil {
			e.Paused = true
		}
		a.downloads.snaps = a.eng.Snapshots()
		a.refreshDownloadItems()
		return a.saveState()
	}
	if err := a.resumeDownload(it); err != nil {
		return a.showError("resume failed: " + err.Error())
	}
	if a.st != nil {
		if e := a.st.Find(it.Magnet); e != nil {
			e.Paused = false
		}
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.refreshDownloadItems()
	return tea.Batch(a.saveState(), a.ensureTick())
}

func (a *App) verifyDownload(it downloadItem) tea.Cmd {
	switch it.State {
	case engine.StateVerifying:
		return a.showError("verification already in progress")
	case engine.StateMissing:
		return a.showError("missing data - relink to existing files first")
	case engine.StateDone, engine.StateSeeding:
	default:
		return a.showError("verification is available after the download completes")
	}

	h := it.Hash
	if !it.Live {
		var err error
		h, err = a.activateDownload(it)
		if err != nil {
			return a.showError("verify failed: " + err.Error())
		}
	}
	if a.st != nil {
		if e := a.st.Find(it.Magnet); e != nil {
			e.Paused = false
		}
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.refreshDownloadItems()
	magnet := it.Magnet
	return tea.Batch(a.saveState(), a.ensureTick(), func() tea.Msg {
		result, err := a.eng.Verify(context.Background(), h)
		return verifyDoneMsg{hash: h, magnet: magnet, result: result, err: err}
	})
}

func (a *App) onVerifyDone(msg verifyDoneMsg) tea.Cmd {
	a.downloads.snaps = a.eng.Snapshots()
	var save tea.Cmd
	if a.st != nil {
		if entry := a.st.Find(msg.magnet); entry != nil && applyVerifyResultToEntry(entry, msg.result) {
			save = a.saveState()
		}
	}
	a.refreshDownloadItems()
	if msg.err != nil {
		a.toast.text = ""
		return tea.Batch(save, a.ensureTick(), a.showError("verify failed: "+msg.err.Error()))
	}

	notice, warn := verificationNotice(msg.result)
	tone := toastOK
	if warn {
		tone = toastWarn
	}
	return tea.Batch(save, a.ensureTick(), a.showToast(notice, tone, toastLong))
}

func applyVerifyResultToEntry(entry *state.Entry, result engine.VerifyResult) bool {
	if !result.NeedsRepair && !result.ChecksumMismatch {
		return false
	}
	changed := entry.Done || entry.CompletedAt != nil || entry.Paused != result.ChecksumMismatch
	entry.Done = false
	entry.CompletedAt = nil
	entry.Paused = result.ChecksumMismatch
	if result.ChecksumMismatch && entry.BytesCompleted != 0 {
		entry.BytesCompleted = 0
		changed = true
	}
	return changed
}

func verificationNotice(result engine.VerifyResult) (string, bool) {
	switch {
	case result.ChecksumMismatch:
		return "checksum failed - moved to " + filepath.Base(result.QuarantinePath), true
	case result.BadPieces == 1:
		return "1 bad piece - redownloading", true
	case result.BadPieces > 1:
		return fmt.Sprintf("%d bad pieces - redownloading", result.BadPieces), true
	case result.NeedsRepair:
		return "incomplete data found - redownloading", true
	default:
		return "verified - all data is valid", false
	}
}

func (a *App) resumeDownload(it downloadItem) error {
	_, err := a.activateDownload(it)
	return err
}

func (a *App) activateDownload(it downloadItem) (metainfo.Hash, error) {
	downloadDir := it.DownloadDir
	if downloadDir == "" {
		downloadDir = a.cfg.DownloadDir
	}
	seed := it.Seed
	opts := engine.AddOptions{DownloadDir: downloadDir, Seed: &seed}
	if a.st != nil {
		if e := a.st.Find(it.Magnet); e != nil {
			opts.Excluded = e.Excluded
		}
	}
	if strings.HasPrefix(it.Magnet, "http://") || strings.HasPrefix(it.Magnet, "https://") {
		name := it.Name
		var checksum engine.Checksum
		expectedSize := int64(0)
		lockToOrigin := false
		if a.st != nil {
			if e := a.st.Find(it.Magnet); e != nil {
				name = e.Name
				var err error
				checksum, err = directEntryChecksum(*e)
				if err != nil {
					return metainfo.Hash{}, err
				}
				expectedSize = e.ExpectedSize
				lockToOrigin = e.LockToOrigin
			}
		}
		return a.eng.AddDirectDownloadWithOptions(engine.DirectDownload{
			URL: it.Magnet, Name: name, Checksum: checksum, ExpectedSize: expectedSize,
			LockToOrigin: lockToOrigin,
		}, opts)
	}
	return a.eng.AddWithOptions(it.Magnet, opts)
}

func directEntryChecksum(e state.Entry) (engine.Checksum, error) {
	if strings.TrimSpace(e.Checksum) != "" || strings.TrimSpace(e.ChecksumAlgorithm) != "" {
		return engine.NewChecksum(e.ChecksumAlgorithm, e.Checksum)
	}
	return engine.SHA256Checksum(e.SHA256)
}

func (a *App) removeDownload(it downloadItem, deleteData bool) error {
	if it.Live {
		if err := a.eng.Remove(it.Hash, false); err != nil {
			return err
		}
	}
	if deleteData {
		if err := deleteDownloadData(it); err != nil {
			return err
		}
	}
	a.st.Remove(it.Magnet)
	return nil
}

func (a *App) moveDownload(it downloadItem, targetDir string) error {
	if strings.TrimSpace(targetDir) == "" {
		return fmt.Errorf("move needs a destination folder")
	}
	if it.DataPath == "" {
		return fmt.Errorf("move needs a known saved path")
	}
	targetDir, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	newPath := filepath.Join(targetDir, filepath.Base(it.DataPath))
	if it.Live {
		if err := a.eng.Remove(it.Hash, false); err != nil {
			return err
		}
	}
	if err := movePayload(it.DataPath, newPath); err != nil {
		return err
	}
	if e := a.st.Find(it.Magnet); e != nil {
		e.DownloadDir = targetDir
		e.DataPath = newPath
		e.NeedsRelink = false
	}
	moved := it
	moved.DownloadDir = targetDir
	moved.DataPath = newPath
	if shouldResumeAfterPathChange(moved) {
		if err := a.resumeDownload(moved); err != nil {
			return err
		}
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.downloads.pathExists[newPath] = true
	delete(a.downloads.pathExists, it.DataPath)
	a.refreshDownloadItems()
	return nil
}

func (a *App) relinkDownload(it downloadItem, targetPath string) error {
	if strings.TrimSpace(targetPath) == "" {
		return fmt.Errorf("relink needs an existing file or folder path")
	}
	targetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetPath); err != nil {
		return err
	}
	isDirect := strings.HasPrefix(it.Magnet, "http://") || strings.HasPrefix(it.Magnet, "https://")
	if !isDirect && it.Name != "" && it.Name != "?" && filepath.Base(targetPath) != it.Name {
		return fmt.Errorf("relink path must end with %q for this torrent", it.Name)
	}
	if it.Live {
		if err := a.eng.Remove(it.Hash, false); err != nil {
			return err
		}
	}
	if e := a.st.Find(it.Magnet); e != nil {
		e.DownloadDir = filepath.Dir(targetPath)
		e.DataPath = targetPath
		e.NeedsRelink = false
		if isDirect {
			e.Name = filepath.Base(targetPath)
		}
		if e.Done && e.CompletedAt == nil {
			now := time.Now().UTC()
			e.CompletedAt = &now
		}
	}
	linked := it
	linked.DownloadDir = filepath.Dir(targetPath)
	linked.DataPath = targetPath
	if shouldResumeAfterPathChange(linked) {
		if err := a.resumeDownload(linked); err != nil {
			return err
		}
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.downloads.pathExists[targetPath] = true
	a.refreshDownloadItems()
	return nil
}

func shouldResumeAfterPathChange(it downloadItem) bool {
	if it.State == engine.StatePaused || it.State == engine.StateMissing {
		return false
	}
	if it.State == engine.StateDone && !it.Seed {
		return false
	}
	return it.Magnet != ""
}

func movePayload(oldPath, newPath string) error {
	if oldPath == "" || newPath == "" || oldPath == newPath {
		return nil
	}
	type move struct{ old, new string }
	var moves []move
	for _, suffix := range []string{"", ".part", ".part.meta"} {
		old, target := oldPath+suffix, newPath+suffix
		if _, err := os.Stat(old); err == nil {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("target already exists: %s", target)
			} else if !os.IsNotExist(err) {
				return err
			}
			moves = append(moves, move{old, target})
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(moves) == 0 {
		return fmt.Errorf("nothing to move at %s", oldPath)
	}
	for i, candidate := range moves {
		if err := os.Rename(candidate.old, candidate.new); err != nil {
			for j := i - 1; j >= 0; j-- {
				_ = os.Rename(moves[j].new, moves[j].old)
			}
			return err
		}
	}
	return nil
}

func deleteDownloadData(it downloadItem) error {
	if it.DataPath == "" {
		return fmt.Errorf("delete data refused: no saved path")
	}
	if !safeDownloadPath(it.DownloadDir, it.DataPath) {
		return fmt.Errorf("delete data refused: unsafe saved path")
	}
	err1 := os.RemoveAll(it.DataPath)
	err2 := os.Remove(it.DataPath + ".part")
	err3 := os.Remove(it.DataPath + ".part.meta")
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	if err3 != nil && !os.IsNotExist(err3) {
		return err3
	}
	return nil
}

func safeDownloadPath(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (a *App) viewDownloads() string {
	d := &a.downloads
	width := a.contentWidth()
	items := a.downloadItems()
	metrics := d.metrics
	if !d.itemsReady {
		metrics = summarizeDownloads(items)
	}

	if len(items) == 0 {
		empty := lipgloss.JoinVertical(lipgloss.Center,
			styleDim.Render(strings.Join(sleepingCat, "\n")),
			"",
			styleFaint.Render("the cat's napping - nothing downloading"),
			"",
			styleDim.Render("search for something, then press enter to preview it"),
		)
		body := lipgloss.Place(width, a.bodyHeight(), lipgloss.Center, lipgloss.Center, empty)
		return a.chrome("downloads", body, hints(hint("esc", "back"), hint("?", "keys")))
	}

	listRows := a.downloadListRows()
	start, end := d.win.clamp(len(items), listRows)

	var b strings.Builder
	b.WriteString(a.downloadsOverview(metrics, width))
	b.WriteString("\n")
	for i := start; i < end; i++ {
		b.WriteString(a.renderDownloadItem(items[i], i == d.win.cursor, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}

	if detail := a.downloadDetail(items[d.win.cursor], width); detail != "" {
		b.WriteString("\n\n" + detail)
	}

	help := a.keyStrip(a.helpBudget(width))
	if d.confirmRemove != nil {
		verb := "remove from list"
		if d.confirmRemove.deleteData {
			verb = "delete data"
		}
		help = styleErr.Render(verb+"?  ") + hints(hint("y/enter", "confirm"), hint("esc", "cancel"))
	}
	if d.prompt.action != pathActionNone {
		help = d.prompt.input.View()
	}
	return a.chrome(a.downloadsContext(metrics), b.String(), help)
}

func (a *App) renderDownloadItem(it downloadItem, selected bool, width int) string {
	marker := "  "
	nameStyle := styleFg
	if selected {
		marker = styleSelBar.Render("▍ ")
		nameStyle = styleBrand
	}
	badge := stateBadge(it.State)
	kind := ""
	if directDownload(it) && width >= 44 {
		kind = styleFaint.Render("direct") + "  "
	}
	glyph := downloadStateGlyph(it.State) + " "
	nameWidth := max(1, width-lipgloss.Width(marker)-lipgloss.Width(glyph)-lipgloss.Width(kind)-lipgloss.Width(badge)-2)
	name := nameStyle.Render(truncate(it.Name, nameWidth))
	line1 := marker + glyph + name
	right := kind + badge
	if gap := width - lipgloss.Width(line1) - lipgloss.Width(right); gap > 0 {
		line1 += strings.Repeat(" ", gap) + right
	} else {
		line1 += " " + right
	}

	pct := fmt.Sprintf("%5.1f%%", it.Progress()*100)
	activity := downloadRowActivity(it)
	activityWidth := min(lipgloss.Width(activity), max(0, width/3))
	if activityWidth > 0 {
		activity = truncate(activity, activityWidth)
	}
	barWidth := width - 4 - lipgloss.Width(pct) - 1
	if activity != "" {
		barWidth -= lipgloss.Width(activity) + 2
	}
	barWidth = max(4, min(44, barWidth))
	line2 := "    " + progressRail(it.Progress(), barWidth) + " " + styleDim.Render(pct)
	if activity != "" {
		line2 += styleFaint.Render("  ") + activity
	}
	return truncate(line1, width) + "\n" + truncate(line2, width)
}

func directDownload(it downloadItem) bool {
	return strings.HasPrefix(it.Magnet, "http://") || strings.HasPrefix(it.Magnet, "https://")
}

func downloadStateGlyph(s engine.TorrentState) string {
	switch s {
	case engine.StateDownloading:
		return styleOK.Render("↓")
	case engine.StateSeeding:
		return styleOK.Render("↑")
	case engine.StateDone:
		return styleOK.Render("✓")
	case engine.StatePaused:
		return styleFaint.Render("Ⅱ")
	case engine.StateMissing:
		return styleErr.Render("!")
	case engine.StateVerifying:
		return styleBest.Render("◇")
	case engine.StateFetchingMeta, engine.StatePreviewing:
		return styleDim.Render("…")
	default:
		return styleDim.Render("○")
	}
}

func downloadRowActivity(it downloadItem) string {
	parts := make([]string, 0, 2)
	if it.SpeedBps > 0 {
		parts = append(parts, styleOK.Render("↓ "+humanSpeed(it.SpeedBps)))
	}
	if it.ETA > 0 {
		parts = append(parts, "ETA "+fmtETA(it.ETA))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	if showDownloadNote(it.Note) {
		return styleFaint.Render(it.Note)
	}
	switch it.State {
	case engine.StateDone:
		return styleOK.Render("ready")
	case engine.StateSeeding:
		return styleOK.Render("complete · seeding")
	case engine.StatePaused:
		return styleFaint.Render("paused")
	case engine.StateMissing:
		return styleErr.Render("file missing")
	case engine.StateVerifying:
		return styleBest.Render("checking data")
	case engine.StateFetchingMeta:
		return styleDim.Render("fetching metadata")
	default:
		return styleDim.Render("waiting")
	}
}

func (it downloadItem) Progress() float64 {
	if it.State == engine.StateDone || it.State == engine.StateSeeding {
		return 1
	}
	if it.Length == 0 {
		return 0
	}
	return clampProgress(float64(it.BytesCompleted) / float64(it.Length))
}

func clampProgress(progress float64) float64 { return max(0, min(1, progress)) }

func progressRail(progress float64, width int) string {
	width = max(1, width)
	filled := int(clampProgress(progress)*float64(width) + 0.5)
	return styleOK.Render(strings.Repeat("━", filled)) + styleRule.Render(strings.Repeat("─", width-filled))
}

func showDownloadNote(note string) bool {
	switch strings.TrimSpace(note) {
	case "", "not active", "paused", "saved in state":
		return false
	default:
		return true
	}
}

func (a *App) downloadsOverview(m downloadMetrics, width int) string {
	counts := []string{fmt.Sprintf("%d total", m.Total)}
	if m.Active > 0 {
		counts = append(counts, styleOK.Render(fmt.Sprintf("%d active", m.Active)))
	}
	if m.Paused > 0 {
		counts = append(counts, styleBest.Render(fmt.Sprintf("%d paused", m.Paused)))
	}
	if m.Done > 0 {
		counts = append(counts, styleOK.Render(fmt.Sprintf("%d complete", m.Done)))
	}
	if m.Missing > 0 {
		counts = append(counts, styleErr.Render(fmt.Sprintf("%d missing", m.Missing)))
	}
	left := styleTitle.Render("queue") + styleFaint.Render("  ") + strings.Join(counts, styleFaint.Render("  ·  "))
	rightParts := []string{}
	if m.SpeedBps > 0 {
		rightParts = append(rightParts, styleOK.Render("↓ "+humanSpeed(m.SpeedBps)))
	}
	if eta := m.ETA(); eta > 0 {
		rightParts = append(rightParts, styleDim.Render("ETA "+fmtETA(eta)))
	}
	line1 := joinDownloadEnds(left, strings.Join(rightParts, "  "), width)

	if a.bodyHeight() < 10 {
		return truncate(line1, width) + "\n" + rule(width)
	}
	percent := fmt.Sprintf("%5.1f%%", m.Progress()*100)
	sizes := ""
	if m.Length > 0 && width >= 62 {
		sizes = humanBytes(m.BytesCompleted) + " / " + humanBytes(m.Length)
	}
	railWidth := width - lipgloss.Width(percent) - 1
	if sizes != "" {
		railWidth -= lipgloss.Width(sizes) + 3
	}
	railWidth = max(4, min(56, railWidth))
	line2 := progressRail(m.Progress(), railWidth) + " " + styleDim.Render(percent)
	if sizes != "" {
		line2 += styleFaint.Render("   " + sizes)
	}

	return truncate(line1, width) + "\n" + truncate(line2, width) + "\n" + rule(width)
}

func joinDownloadEnds(left, right string, width int) string {
	if right == "" || lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		return truncate(left, width)
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func (a *App) downloadDetail(it downloadItem, width int) string {
	if a.bodyHeight() < 20 || width < 44 {
		return ""
	}
	path := it.DataPath
	if path == "" {
		path = "(unknown path)"
	}
	mode := "torrent"
	if directDownload(it) {
		mode = "direct"
	}
	info := []string{mode}
	if it.Length > 0 {
		info = append(info, humanBytes(it.Length))
	}
	if it.PeersTotal > 0 {
		info = append(info, fmt.Sprintf("peers %d/%d", it.PeersActive, it.PeersTotal))
	}
	if it.Seeders > 0 {
		info = append(info, plural(it.Seeders, "seeder"))
	}
	if it.Trackers > 0 {
		info = append(info, plural(it.Trackers, "tracker"))
	}
	if it.ProxyStrict {
		info = append(info, "strict proxy")
	} else if it.DHTEnabled && mode == "torrent" {
		info = append(info, "DHT on")
	}
	actions := downloadActionHint(it)
	innerWidth := max(1, width-4)
	headerRight := stateBadge(it.State)
	headerLeft := styleBrand.Render("selected") + styleFaint.Render("  ") + styleFg.Bold(true).Render(truncate(it.Name, max(1, innerWidth-lipgloss.Width(headerRight)-2)))
	lines := []string{
		joinDownloadEnds(headerLeft, headerRight, innerWidth),
		styleFaint.Render("save     ") + styleDim.Render(truncate(path, max(1, innerWidth-9))),
		styleFaint.Render("info     ") + styleDim.Render(truncate(strings.Join(info, " · "), max(1, innerWidth-9))),
		styleFaint.Render("actions  ") + styleDim.Render(truncate(actions, max(1, innerWidth-9))),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(max(1, width-4)).
		Render(strings.Join(lines, "\n"))
}

func downloadActionHint(it downloadItem) string {
	switch it.State {
	case engine.StatePaused:
		return "p resume · m move · x remove"
	case engine.StateMissing:
		return "r relink · m move · x remove"
	case engine.StateDone, engine.StateSeeding:
		if revealAvailable {
			return "enter open · o reveal · v verify · y copy path"
		}
		return "enter open · v verify · y copy path"
	case engine.StateVerifying:
		return "verification in progress"
	default:
		return "p pause · m move · x remove · ? all keys"
	}
}

func (a *App) downloadListRows() int {
	body := a.bodyHeight()
	overviewLines := 2
	if body >= 10 {
		overviewLines = 3
	}
	detailLines := 0
	if body >= 20 && a.contentWidth() >= 44 {
		detailLines = 7 // one breathing line plus the six-line bordered card
	}
	return max(1, (body-overviewLines-detailLines)/2)
}

// downloadsContext summarises the list in the header: what is moving, what is
// waiting on you, and what is finished, so the shape of the queue is readable
// without counting rows.
func (a *App) downloadsContext(m downloadMetrics) string {
	if m.Active == 0 && m.Paused == 0 && m.Missing == 0 && m.Done > 0 {
		return "downloads · all done"
	}
	parts := []string{}
	if m.Active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", m.Active))
	}
	if m.Paused > 0 {
		parts = append(parts, fmt.Sprintf("%d paused", m.Paused))
	}
	if m.Missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", m.Missing))
	}
	if m.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", m.Done))
	}
	if len(parts) == 0 {
		return "downloads"
	}
	return "downloads · " + strings.Join(parts, " · ")
}

// activityChip is the header's live transfer summary. It exists because
// queuing a download no longer jumps to the downloads screen: without it, a
// download started from the results list would vanish from view entirely.
func (a *App) activityChip() string {
	m := a.downloads.metrics
	switch {
	case m.Active > 0:
		return styleOK.Render(fmt.Sprintf("↓ %d", m.Active)) + styleDim.Render("  "+humanSpeed(m.SpeedBps))
	case m.Seeding > 0:
		return styleFaint.Render(fmt.Sprintf("↑ %d seeding", m.Seeding))
	default:
		return ""
	}
}

// stateBadge renders a torrent state with a state-appropriate color.
func stateBadge(s engine.TorrentState) string {
	switch s {
	case engine.StateSeeding:
		return styleOK.Render("seeding")
	case engine.StateDone:
		return styleOK.Render(s.String())
	case engine.StatePaused:
		return styleFaint.Render(s.String())
	case engine.StateMissing:
		return styleErr.Render(s.String())
	case engine.StateVerifying:
		return styleBest.Render(s.String())
	case engine.StateFetchingMeta, engine.StatePreviewing:
		return styleDim.Render(s.String())
	}
	return styleStateTag.Render(s.String())
}
