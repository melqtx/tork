package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

const finderRevealAvailable = runtime.GOOS == "darwin"

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
	State          engine.TorrentState
	Note           string
	Seed           bool
	Live           bool
	EntryIndex     int
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
	snaps   []engine.Snapshot
	win     listWindow
	bar     progress.Model
	ticking bool

	confirmRemove *removeConfirm
	prompt        pathPrompt
}

func newDownloadsModel() downloadsModel {
	bar := progress.New(progress.WithSolidFill(string(colBrand)))
	bar.EmptyColor = string(colBorder)
	bar.Width = 40
	bar.ShowPercentage = false
	return downloadsModel{bar: bar}
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
	switch key.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		a.screen = screenSearch
		return a, a.search.input.Focus()
	case "up", "k":
		d.win.move(-1, len(items), rows)
	case "down", "j":
		d.win.move(1, len(items), rows)
	case "pgup":
		d.win.move(-rows, len(items), rows)
	case "pgdown":
		d.win.move(rows, len(items), rows)
	case "g", "home":
		d.win.home()
	case "G", "end":
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
	case "o":
		if finderRevealAvailable {
			it, ok := a.selectedDownload(items)
			if !ok {
				break
			}
			return a, revealDownload(it)
		}
	}
	return a, nil
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
	return func() tea.Msg {
		path := strings.TrimSpace(it.DataPath)
		if path == "" {
			return revealDownloadMsg{err: fmt.Errorf("reveal needs a known saved path")}
		}
		if err := exec.Command("open", "-R", path).Run(); err != nil {
			return revealDownloadMsg{err: fmt.Errorf("reveal failed: %w", err)}
		}
		return revealDownloadMsg{}
	}
}

// yankToast is the small confirmation box flashed in the bottom-right corner
// after a copy lands on the clipboard.
func yankToast(what string) string {
	return styleYankBox.Render(styleBrand.Render("yanked ") + styleDim.Render(what))
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
			a.errText = "download is no longer in the list"
			return a, clearErrCmd()
		}
		var err error
		if action == pathActionMove {
			err = a.moveDownload(it, target)
		} else {
			err = a.relinkDownload(it, target)
		}
		if err != nil {
			a.errText = err.Error()
			return a, clearErrCmd()
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
	d.confirmRemove = nil
	switch key.String() {
	case "esc", "n":
		return a, nil
	case "y", "enter":
		if err := a.removeDownload(conf.item, conf.deleteData); err != nil {
			a.errText = "remove failed: " + err.Error()
			return a, clearErrCmd()
		}
		d.snaps = a.eng.Snapshots()
		d.win.clamp(len(a.downloadItems()), a.downloadListRows())
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

func itemFromSnapshot(s engine.Snapshot, idx int, live bool) downloadItem {
	return downloadItem{
		Hash: s.Hash, Magnet: s.Magnet, Name: s.Name,
		DownloadDir: s.DownloadDir, DataPath: s.DataPath,
		BytesCompleted: s.BytesCompleted, Length: s.Length,
		SpeedBps: s.SpeedBps, ETA: s.ETA,
		PeersActive: s.PeersActive, PeersTotal: s.PeersTotal,
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
	switch {
	case e.NeedsRelink:
		st = engine.StateMissing
		note = "save path unknown - relink before retrying"
	case e.Done && !entryPathExists(e.DataPath):
		st = engine.StateMissing
		note = "files not found - relink or move before retrying"
	case e.Done:
		st = engine.StateDone
		note = "saved in state"
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
		a.errText = "direct downloads cannot seed"
		return clearErrCmd()
	}
	next := !it.Seed
	if it.Live {
		a.eng.SetSeeding(it.Hash, next)
		a.downloads.snaps = a.eng.Snapshots()
	}
	if e := a.st.Find(it.Magnet); e != nil {
		e.Seed = state.Bool(next)
		return a.saveState()
	}
	return nil
}

func (a *App) togglePause(it downloadItem) tea.Cmd {
	if it.State == engine.StateMissing {
		a.errText = "missing data - press r to relink, or d to delete it"
		return clearErrCmd()
	}
	if it.Live && it.State != engine.StatePaused {
		a.eng.Pause(it.Hash)
		if e := a.st.Find(it.Magnet); e != nil {
			e.Paused = true
		}
		a.downloads.snaps = a.eng.Snapshots()
		return a.saveState()
	}
	if err := a.resumeDownload(it); err != nil {
		a.errText = "resume failed: " + err.Error()
		return clearErrCmd()
	}
	if e := a.st.Find(it.Magnet); e != nil {
		e.Paused = false
	}
	a.downloads.snaps = a.eng.Snapshots()
	return tea.Batch(a.saveState(), a.ensureTick())
}

func (a *App) verifyDownload(it downloadItem) tea.Cmd {
	if it.State == engine.StateMissing {
		a.errText = "missing data - relink to existing files first"
		return clearErrCmd()
	}
	if it.Live {
		if err := a.eng.Remove(it.Hash, false); err != nil {
			a.errText = "verify failed: " + err.Error()
			return clearErrCmd()
		}
	}
	if err := a.resumeDownload(it); err != nil {
		a.errText = "verify failed: " + err.Error()
		return clearErrCmd()
	}
	if e := a.st.Find(it.Magnet); e != nil {
		e.Paused = false
	}
	a.downloads.snaps = a.eng.Snapshots()
	return tea.Batch(a.saveState(), a.ensureTick())
}

func (a *App) resumeDownload(it downloadItem) error {
	downloadDir := it.DownloadDir
	if downloadDir == "" {
		downloadDir = a.cfg.DownloadDir
	}
	seed := it.Seed
	opts := engine.AddOptions{DownloadDir: downloadDir, Seed: &seed}
	if e := a.st.Find(it.Magnet); e != nil {
		opts.Excluded = e.Excluded
	}
	if strings.HasPrefix(it.Magnet, "http://") || strings.HasPrefix(it.Magnet, "https://") {
		name := it.Name
		sum := ""
		if e := a.st.Find(it.Magnet); e != nil {
			name = e.Name
			sum = e.SHA256
		}
		_, err := a.eng.AddDirectWithOptions(it.Magnet, name, sum, opts)
		return err
	}
	_, err := a.eng.AddWithOptions(it.Magnet, opts)
	return err
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
		return a.resumeDownload(moved)
	}
	a.downloads.snaps = a.eng.Snapshots()
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
		return a.resumeDownload(linked)
	}
	a.downloads.snaps = a.eng.Snapshots()
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
	movedAny := false
	if _, err := os.Stat(oldPath); err == nil {
		if _, err := os.Stat(newPath); err == nil {
			return fmt.Errorf("target already exists: %s", newPath)
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		movedAny = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(oldPath + ".part"); err == nil {
		if _, err := os.Stat(newPath + ".part"); err == nil {
			return fmt.Errorf("target already exists: %s", newPath+".part")
		}
		if err := os.Rename(oldPath+".part", newPath+".part"); err != nil {
			return err
		}
		movedAny = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if !movedAny {
		return fmt.Errorf("nothing to move at %s", oldPath)
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
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
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

func entryPathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (a *App) viewDownloads() string {
	d := &a.downloads
	width := a.contentWidth()
	items := a.downloadItems()

	if len(items) == 0 {
		empty := lipgloss.JoinVertical(lipgloss.Center,
			styleDim.Render(strings.Join(sleepingCat, "\n")),
			"",
			styleFaint.Render("the cat's napping - nothing downloading"),
			"",
			styleDim.Render("press ")+styleKey.Render("tab")+styleDim.Render(" to go hunting"),
		)
		body := lipgloss.Place(width, a.bodyHeight(), lipgloss.Center, lipgloss.Center, empty)
		return a.chrome("downloads", body, hints(hint("tab", "screens"), hint("q", "quit")))
	}

	d.bar.Width = max(20, min(64, width-40))
	listRows := a.downloadListRows()
	start, end := d.win.clamp(len(items), listRows)

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(a.renderDownloadItem(items[i], i == d.win.cursor, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	for i := end - start; i < listRows; i++ {
		b.WriteString("\n\n\n")
	}

	if detail := a.downloadDetail(items[d.win.cursor], width); detail != "" {
		b.WriteString("\n" + rule(width) + "\n" + detail)
	}

	helpParts := []string{hint("↑↓", "move"), hint("p", "pause"), hint("s", "seed"), hint("v", "verify"), hint("m", "move"), hint("r", "relink"), hint("y/Y", "copy"), hint("x", "remove"), hint("d", "delete"), hint("H", "health"), hint("esc", "search")}
	if finderRevealAvailable {
		helpParts = append(helpParts, hint("o", "reveal"))
	}
	help := hints(helpParts...)
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
	body := b.String()
	if a.yanked != "" {
		body = overlayBottomRight(padLines(body, a.bodyHeight()), yankToast(a.yanked), width)
	}
	return a.chrome(a.downloadsContext(items), body, help)
}

func (a *App) renderDownloadItem(it downloadItem, selected bool, width int) string {
	marker := "  "
	nameStyle := styleFg
	if selected {
		marker = styleSelBar.Render("▍ ")
		nameStyle = lipgloss.NewStyle().Foreground(colBrand).Bold(true)
	}
	name := marker + nameStyle.Render(truncate(it.Name, max(20, width-6)))
	if it.State == engine.StateMissing {
		name += " " + styleErr.Render("missing")
	}
	pct := fmt.Sprintf("%5.1f%%", it.Progress()*100)
	bar := "  " + a.downloads.bar.ViewAs(it.Progress()) + "  " + styleDim.Render(pct)
	stats := fmt.Sprintf("  %s / %s   %s   ETA %s   %s %d/%d   %s",
		humanBytes(it.BytesCompleted),
		humanBytes(it.Length),
		humanSpeed(it.SpeedBps),
		fmtETA(it.ETA),
		styleFaint.Render("peers"), it.PeersActive, it.PeersTotal,
		stateBadge(it.State),
	)
	if it.Note != "" {
		stats += "   " + styleFaint.Render(it.Note)
	}
	return name + "\n" + bar + "\n" + styleDim.Render(stats) + "\n"
}

func (it downloadItem) Progress() float64 {
	if it.Length == 0 {
		if it.State == engine.StateDone {
			return 1
		}
		return 0
	}
	return float64(it.BytesCompleted) / float64(it.Length)
}

func (a *App) downloadDetail(it downloadItem, width int) string {
	if a.bodyHeight() < 14 {
		return ""
	}
	path := it.DataPath
	if path == "" {
		path = "(unknown path)"
	}
	seed := "off"
	if it.Seed {
		seed = "on"
	}
	keys := "m move folder · r relink existing files · y copy full path · Y copy magnet · d delete data"
	if finderRevealAvailable {
		keys += " · o reveal in Finder"
	}
	lines := []string{
		styleFaint.Render("path  ") + styleDim.Render(truncate(path, width-7)),
		styleFaint.Render("root  ") + styleDim.Render(truncate(it.DownloadDir, width-7)),
		styleFaint.Render("seed  ") + styleDim.Render(seed) + styleFaint.Render("   status  ") + stateBadge(it.State),
		styleFaint.Render("size  ") + styleDim.Render(fmt.Sprintf("%s selected", humanBytes(it.Length))),
		styleFaint.Render("keys  ") + styleDim.Render(truncate(keys, width-7)),
	}
	return strings.Join(lines, "\n")
}

func (a *App) downloadListRows() int {
	body := a.bodyHeight()
	if body >= 14 {
		body -= 6
	}
	return max(1, body/4)
}

func (a *App) downloadsContext(items []downloadItem) string {
	ctx := "downloads"
	allDone := true
	for _, it := range items {
		if it.State != engine.StateSeeding && it.State != engine.StateDone {
			allDone = false
			break
		}
	}
	if allDone {
		ctx = "downloads · all done"
	}
	return ctx
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
	case engine.StateFetchingMeta, engine.StatePreviewing:
		return styleDim.Render(s.String())
	}
	return styleStateTag.Render(s.String())
}
