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
	hash      metainfo.Hash
	magnet    string
	name      string
	from      screen
	owned     bool
	files     []engine.FileInfo
	tree      *fileNode
	rows      []*fileNode
	ready     bool
	startedAt time.Time
	excluded  map[int]bool // keyed by FileInfo.Index
	win       listWindow
}

func newPreviewModel(h metainfo.Hash, magnet, name string, from screen, owned bool) previewModel {
	return previewModel{
		hash: h, magnet: magnet, name: previewName(magnet, name), from: from,
		owned: owned, startedAt: time.Now(), excluded: map[int]bool{},
	}
}

// refresh polls the engine for file metadata once it arrives.
func (p *previewModel) refresh(eng *engine.Engine) {
	if p.ready {
		return
	}
	if files, ok := eng.Files(p.hash); ok {
		p.files = files
		p.tree = buildFileTree(files)
		p.rebuildRows()
		if inferred := p.inferredName(); inferred != "" && strings.HasPrefix(p.name, "magnet · ") {
			p.name = inferred
		}
		p.ready = true
	}
}

func (p *previewModel) rebuildRows() {
	p.rows = p.rows[:0]
	if p.tree != nil {
		p.tree.flatten(&p.rows)
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
		if p.owned {
			a.eng.Remove(p.hash, false) // discard only metadata-only torrents we added
		}
		a.screen = p.from
		return a, nil
	}
	if !p.ready {
		// Metadata can take a while for a bare hash because a peer must supply
		// it. Enter lets the user approve the whole torrent now and watch peer
		// discovery from Downloads; waiting preserves file-by-file selection.
		if key.String() == "enter" && p.hash != (metainfo.Hash{}) {
			return a, a.startPreviewDownload()
		}
		return a, nil
	}
	rows := a.previewRows()
	switch key.String() {
	case "up", "k":
		p.win.move(-1, len(p.rows), rows)
	case "down", "j":
		p.win.move(1, len(p.rows), rows)
	case "pgup":
		p.win.move(-rows, len(p.rows), rows)
	case "pgdown":
		p.win.move(rows, len(p.rows), rows)
	case "g", "home":
		p.win.home()
	case "G", "end":
		p.win.end(len(p.rows), rows)
	case "left", "h":
		if n := p.currentNode(); n != nil && n.fileIdx < 0 {
			n.collapsed = true
			p.rebuildRows()
			p.win.clamp(len(p.rows), rows)
		}
	case "right", "l":
		if n := p.currentNode(); n != nil && n.fileIdx < 0 {
			n.collapsed = false
			p.rebuildRows()
			p.win.clamp(len(p.rows), rows)
		}
	case " ":
		if n := p.currentNode(); n != nil {
			p.toggleNode(n)
		}
	case "a": // include all
		p.excluded = map[int]bool{}
	case "n": // exclude all
		for _, f := range p.files {
			p.excluded[f.Index] = true
		}
	case "enter":
		if n := p.currentNode(); n != nil && n.fileIdx < 0 {
			n.collapsed = !n.collapsed
			p.rebuildRows()
			p.win.clamp(len(p.rows), rows)
			return a, nil
		}
		if p.selectedBytes() == 0 {
			a.errText = "select at least one file"
			return a, clearErrCmd()
		}
		return a, a.startPreviewDownload()
	}
	return a, nil
}

func (p *previewModel) toggleNode(n *fileNode) {
	if n.fileIdx >= 0 {
		if p.excluded[n.fileIdx] {
			delete(p.excluded, n.fileIdx)
		} else {
			p.excluded[n.fileIdx] = true
		}
		return
	}
	var leaves []int
	n.leafIndices(&leaves)
	if nodeCheck(n, p.excluded) != checkNone {
		for _, idx := range leaves {
			p.excluded[idx] = true
		}
		return
	}
	for _, idx := range leaves {
		delete(p.excluded, idx)
	}
}

func (p *previewModel) currentNode() *fileNode {
	if p.win.cursor < 0 || p.win.cursor >= len(p.rows) {
		return nil
	}
	return p.rows[p.win.cursor]
}

func (p *previewModel) inferredName() string {
	if p.tree == nil || len(p.tree.children) == 0 {
		return ""
	}
	if len(p.tree.children) == 1 {
		return p.tree.children[0].name
	}
	return p.name
}

func previewName(magnet, name string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	hash := magnetHashPrefix(magnet)
	if hash == "" {
		return "magnet"
	}
	return "magnet · " + hash
}

func magnetHashPrefix(magnet string) string {
	const key = "btih:"
	lower := strings.ToLower(magnet)
	i := strings.Index(lower, key)
	if i < 0 {
		return ""
	}
	start := i + len(key)
	end := start
	for end < len(magnet) && magnet[end] != '&' {
		end++
	}
	hash := magnet[start:end]
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return hash
}

// startPreviewDownload commits the file selection and jumps to downloads.
func (a *App) startPreviewDownload() tea.Cmd {
	p := &a.preview
	excluded := p.excludedSlice()
	a.eng.StartDownload(p.hash, excluded)
	entry := state.Entry{
		Magnet:      p.magnet,
		Name:        p.name,
		AddedAt:     time.Now().UTC(),
		Excluded:    excluded,
		DownloadDir: a.cfg.DownloadDir,
		Seed:        state.Bool(a.cfg.SeedAfterComplete),
	}
	if snap, ok := a.eng.Snapshot(p.hash); ok {
		applySnapshotToEntry(&entry, snap)
	}
	a.st.Upsert(entry)
	a.screen = screenDownloads
	a.downloads.snaps = a.eng.Snapshots()
	return tea.Batch(a.saveState(), a.ensureTick())
}

func (a *App) viewPreview() string {
	p := &a.preview
	width := a.contentWidth()

	if !p.ready {
		waited := time.Since(p.startedAt).Round(time.Second)
		status, hasStatus := a.eng.MetadataDiscovery(p.hash)
		if hasStatus && status.Waiting > 0 {
			waited = status.Waiting.Round(time.Second)
		}
		if waited < 0 {
			waited = 0
		}
		discovery := waited.String()
		if hasStatus && status.Summary() != "" {
			discovery = status.Summary()
		}
		detail := styleFaint.Render("needs a peer with metadata; may take a moment")
		if waited >= 10*time.Second {
			detail = styleFaint.Render("still looking; press enter to queue the whole torrent now")
		}
		msg := lipgloss.JoinVertical(lipgloss.Center,
			styleFg.Render(truncate(p.name, width-4)),
			"",
			styleDim.Render("finding metadata peers…"),
			styleFaint.Render(discovery),
			detail,
		)
		body := lipgloss.Place(width, a.bodyHeight(), lipgloss.Center, lipgloss.Center, msg)
		return a.chrome("preview", body, hints(hint("enter", "queue all now"), hint("esc", "cancel")))
	}

	var b strings.Builder
	source := ""
	if status, ok := a.eng.MetadataDiscovery(p.hash); ok && status.SourceLabel() != "" {
		source = styleFaint.Render(" · " + status.SourceLabel())
	}
	nameWidth := max(12, width-40-lipgloss.Width(source))
	b.WriteString(" " + styleFg.Render(truncate(p.name, nameWidth)) +
		styleFaint.Render(fmt.Sprintf("   %d files · %d dirs", len(p.files), countDirs(p.tree))) +
		styleFaint.Render(" · total ") + styleDim.Render(humanBytes(p.totalBytes())) + source + "\n")
	flagged := ""
	if n := p.flaggedCount(); n > 0 {
		flagged = styleFaint.Render(" · ") + styleHealthMid.Render(fmt.Sprintf("⚠ %d flagged", n))
	}
	b.WriteString(" " + styleOK.Render(fmt.Sprintf("selected %d of %d", p.selectedFiles(), len(p.files))) +
		styleFaint.Render(" · ") + styleDim.Render(humanBytes(p.selectedBytes())) + flagged + "\n\n")

	lay := newPreviewLayout(width)
	maxFile := p.largestFile()
	b.WriteString(renderWindow(&p.win, len(p.rows), a.previewRows(), width, func(i int, selected bool) string {
		return p.renderNode(p.rows[i], lay, maxFile)
	}))

	toggle := "toggle"
	if n := p.currentNode(); n != nil && n.fileIdx < 0 {
		toggle = "toggle dir"
	}
	help := hints(hint("space", toggle), hint("←→", "expand/collapse"), hint("a", "all"), hint("n", "none"), hint("enter", "download"), hint("esc", "cancel"))
	return a.chrome("preview", b.String(), help)
}

func (p *previewModel) flaggedCount() int {
	total := 0
	for _, f := range p.files {
		if riskFor(f.Path) != "" {
			total++
		}
	}
	return total
}

func (p *previewModel) largestFile() int64 {
	var maxLen int64
	for _, f := range p.files {
		if f.Length > maxLen {
			maxLen = f.Length
		}
	}
	return maxLen
}

func (p *previewModel) fileByIndex(idx int) (engine.FileInfo, bool) {
	for _, f := range p.files {
		if f.Index == idx {
			return f, true
		}
	}
	return engine.FileInfo{}, false
}

func (p *previewModel) selectedFiles() int {
	total := 0
	for _, f := range p.files {
		if !p.excluded[f.Index] {
			total++
		}
	}
	return total
}

func (p *previewModel) checkbox(n *fileNode) string {
	switch nodeCheck(n, p.excluded) {
	case checkAll:
		return styleOK.Render("[✓]")
	case checkMixed:
		return styleHealthMid.Render("[-]")
	default:
		return styleFaint.Render("[ ]")
	}
}

func (p *previewModel) renderNode(n *fileNode, lay previewLayout, maxFile int64) string {
	icon, _ := fileKind(n.name)
	size := n.length
	bar := ""
	risk := ""
	if n.fileIdx >= 0 {
		if f, ok := p.fileByIndex(n.fileIdx); ok {
			size = f.Length
			bar = fileSizeBar(size, maxFile, lay.barW)
			if riskFor(f.Path) != "" {
				risk = styleHealthMid.Render("⚠")
			}
		}
	} else {
		if n.collapsed {
			icon = "▸"
		} else {
			icon = "▾"
		}
		bar = styleFaint.Render(strings.Repeat("░", lay.barW))
	}
	nameW := lay.nameW - n.depth*2 - 2
	if nameW < 8 {
		nameW = 8
	}
	name := strings.Repeat("  ", n.depth) + icon + " " + truncate(n.name, nameW)
	line := fmt.Sprintf("%s  %s %s %s %*s",
		p.checkbox(n),
		padRight(name, lay.nameW),
		padRight(risk, 1),
		bar,
		lay.sizeW, humanBytes(size),
	)
	if n.fileIdx >= 0 && p.excluded[n.fileIdx] {
		line = styleFaint.Render(line)
	}
	return line
}

// previewRows is the file-list height on the preview screen (two header lines + blank).
func (a *App) previewRows() int {
	return max(1, a.bodyHeight()-3)
}

func fileSizeBar(size, maxSize int64, cells int) string {
	if cells < 1 {
		return ""
	}
	if maxSize <= 0 || size <= 0 {
		return styleFaint.Render(strings.Repeat("░", cells))
	}
	filled := int((size*int64(cells) + maxSize - 1) / maxSize)
	filled = max(1, min(cells, filled))
	return styleSeeders.Render(strings.Repeat("▓", filled)) +
		styleFaint.Render(strings.Repeat("░", cells-filled))
}

func fileKind(name string) (icon, label string) {
	ext := strings.ToLower(pathExt(name))
	switch ext {
	case ".mkv", ".mp4", ".avi", ".mov", ".webm", ".wmv", ".flv", ".m4v":
		return "▶", "video"
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".wav":
		return "♪", "audio"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".xz", ".bz2":
		return "▤", "archive"
	case ".srt", ".ass", ".ssa", ".vtt":
		return "✉", "subs"
	case ".iso", ".img":
		return "◉", "disc"
	case ".exe", ".bat", ".cmd", ".scr", ".msi", ".vbs", ".ps1", ".js", ".jse", ".wsf", ".jar", ".apk", ".lnk", ".dmg", ".pkg", ".sh":
		return "⚙", "exec"
	default:
		return "·", "file"
	}
}

func riskFor(name string) string {
	lower := strings.ToLower(pathBase(name))
	ext := pathExt(lower)
	if !dangerExt[ext] {
		return ""
	}
	if hasMediaDisguise(lower) {
		return "double-extension executable"
	}
	return "executable or installer"
}

var dangerExt = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".scr": true, ".msi": true,
	".vbs": true, ".ps1": true, ".js": true, ".jse": true, ".wsf": true,
	".jar": true, ".apk": true, ".lnk": true, ".dmg": true, ".pkg": true,
	".sh": true,
}

var mediaExt = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".webm": true,
	".mp3": true, ".flac": true, ".m4a": true, ".srt": true, ".ass": true,
}

func hasMediaDisguise(name string) bool {
	trimmed := strings.TrimSuffix(name, pathExt(name))
	for {
		ext := pathExt(trimmed)
		if ext == "" {
			return false
		}
		if mediaExt[ext] {
			return true
		}
		trimmed = strings.TrimSuffix(trimmed, ext)
	}
}

func pathBase(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func pathExt(name string) string {
	base := pathBase(name)
	if i := strings.LastIndex(base, "."); i >= 0 {
		return base[i:]
	}
	return ""
}
