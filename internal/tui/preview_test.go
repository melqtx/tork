package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/intake"
	"github.com/melqtx/tork/internal/state"
)

func TestDetectTorrentInput(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		kind     intake.Kind
		name     string
		contains string
	}{
		{"magnet:?xt=urn:btih:ABCDEF&dn=My+File", true, intake.Magnet, "My File", "magnet:?"},
		{"0123456789abcdef0123456789abcdef01234567", true, intake.InfoHash, "", "magnet:?xt=urn:btih:"},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", true, intake.InfoHash, "", "magnet:?xt=urn:btih:"},
		{"https://example.test/path/My%20File.torrent", true, intake.TorrentURL, "My File.torrent", "https://"},
		{"dune 2024", false, 0, "", ""},
	}
	for _, c := range cases {
		target, ok, err := intake.DetectHome(c.in)
		if err != nil || ok != c.ok || target.Kind != c.kind || target.Name != c.name {
			t.Errorf("DetectHome(%q) = (%+v,%v,%v), want ok=%v kind=%v name=%q", c.in, target, ok, err, c.ok, c.kind, c.name)
		}
		if c.contains != "" && !strings.Contains(target.Value, c.contains) {
			t.Errorf("target %q does not contain %q", target.Value, c.contains)
		}
	}
}

func TestOpenTorrentQueuesStartupPreview(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, nil, &state.State{}, nil)
	magnet := "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Quiet+Launch"
	if err := a.OpenTorrent(magnet); err != nil {
		t.Fatal(err)
	}
	if a.startup == nil {
		t.Fatal("OpenTorrent did not queue a startup preview")
	}
	if got := a.search.input.Value(); got != magnet {
		t.Fatalf("search input = %q, want original magnet", got)
	}
	if err := a.OpenTorrent("not a torrent"); err == nil {
		t.Fatal("OpenTorrent accepted ordinary search text")
	}
	local := filepath.Join(t.TempDir(), "local.torrent")
	if err := os.WriteFile(local, []byte("metainfo is decoded by the engine command"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenTorrent(local); err != nil {
		t.Fatal(err)
	}
	if a.startup == nil {
		t.Fatal("OpenTorrent did not queue local file preview")
	}
}

func TestPreviewCheckboxDoesNotDuplicateISOIcon(t *testing.T) {
	p := previewModel{
		files:    []engine.FileInfo{{Index: 0, Path: "archlinux.iso", Length: 1}},
		excluded: map[int]bool{},
	}
	n := &fileNode{name: "archlinux.iso", fileIdx: 0, length: 1}
	if got := p.checkbox(n); !strings.Contains(got, "[✓]") {
		t.Fatalf("selected checkbox = %q, want an explicit check mark", got)
	}
	if got := p.renderNode(n, newPreviewLayout(100), 1); strings.Count(got, "◉") != 1 {
		t.Fatalf("ISO row = %q, want exactly one disc icon", got)
	}
}

func TestEnterQueuesMagnetBeforeMetadataArrives(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	magnet := "magnet:?xt=urn:btih:9999999999999999999999999999999999999999"
	h, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		cfg: cfg, eng: eng, st: &state.State{}, screen: screenPreview,
		preview: newPreviewModel(h, magnet, "", screenSearch, owned),
	}
	if a.preview.ready {
		t.Fatal("test torrent unexpectedly has metadata")
	}
	if _, cmd := a.updatePreview(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter while waiting for metadata returned no queue command")
	}
	if a.screen != screenDownloads || len(a.st.Entries) != 1 {
		t.Fatalf("screen = %v, state = %+v; want queued download", a.screen, a.st)
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State == engine.StatePreviewing {
		t.Fatalf("snapshot = %+v, ok=%v; torrent remained preview-only", snap, ok)
	}
}

// The common torrent is one folder holding the payload, so the cursor opens on
// a folder row with everything already selected. Enter there must download, not
// fold, or every download costs an extra hop onto a file row first.
func TestPreviewEnterOnFolderStartsDownload(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	magnet := "magnet:?xt=urn:btih:2222222222222222222222222222222222222222"
	h, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		cfg: cfg, eng: eng, st: &state.State{}, screen: screenPreview,
		preview: newPreviewModel(h, magnet, "Movie", screenResults, owned),
	}
	p := &a.preview
	p.files = []engine.FileInfo{
		{Index: 0, Path: "Movie/movie.mp4", Length: 1 << 30},
		{Index: 1, Path: "Movie/poster.jpg", Length: 52 << 10},
	}
	p.tree = buildFileTree(p.files)
	p.rebuildRows()
	p.ready = true

	if n := p.currentNode(); n == nil || n.fileIdx >= 0 {
		t.Fatalf("cursor opened on %+v, want the folder row", n)
	}
	if _, cmd := a.updatePreview(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter on a folder returned no download command")
	}
	if p.rows[0].collapsed {
		t.Fatal("enter folded the folder instead of downloading it")
	}
	if len(a.st.Entries) != 1 {
		t.Fatalf("state = %+v; want the whole folder queued", a.st.Entries)
	}
	// Queuing returns to the list the preview was opened from, so the next
	// result is one keypress away rather than a trip through downloads.
	if a.screen != screenResults {
		t.Fatalf("screen = %v, want a return to the results list", a.screen)
	}
	if a.toast.text == "" {
		t.Fatal("queuing gave no confirmation toast")
	}
	if snap, ok := eng.Snapshot(h); !ok || snap.State == engine.StatePreviewing {
		t.Fatalf("snapshot = %+v, ok=%v; torrent never left preview", snap, ok)
	}
}

func TestJunkFilesSkipsExtrasButKeepsPayload(t *testing.T) {
	const gib = 1 << 30
	movie := []engine.FileInfo{
		{Index: 0, Path: "Movie/movie.mkv", Length: 2 * gib},
		{Index: 1, Path: "Movie/www.YTS.MX.jpg", Length: 52 << 10},
		{Index: 2, Path: "Movie/RARBG.txt", Length: 30},
		{Index: 3, Path: "Movie/movie.nfo", Length: 4 << 10},
		{Index: 4, Path: "Movie/Sample/sample.mkv", Length: 40 << 20},
		{Index: 5, Path: "Movie/movie.en.srt", Length: 60 << 10},
		{Index: 6, Path: "Movie/cover.jpg", Length: 900 << 10},
	}
	skip := junkFiles(movie)
	for _, idx := range []int{1, 2, 3, 4} {
		if !skip[idx] {
			t.Errorf("%s stayed selected, want it skipped as an extra", movie[idx].Path)
		}
	}
	// Subtitles and artwork are things people actually want; only names that
	// advertise a tracker or a sample are fair game.
	for _, idx := range []int{0, 5, 6} {
		if skip[idx] {
			t.Errorf("%s was skipped, want it kept", movie[idx].Path)
		}
	}

	// A torrent that is nothing but images is a photo set, not a pile of ads.
	photos := []engine.FileInfo{
		{Index: 0, Path: "set/www.host.com-01.jpg", Length: 2 << 20},
		{Index: 1, Path: "set/www.host.com-02.jpg", Length: 2 << 20},
	}
	if got := junkFiles(photos); len(got) != 0 {
		t.Errorf("junkFiles skipped %d of %d files in an all-extras torrent", len(got), len(photos))
	}

	// Size is a veto, never a reason: a big file keeps its selection even when
	// its name looks like an extra.
	bigSample := []engine.FileInfo{
		{Index: 0, Path: "pack/sample.mkv", Length: gib},
		{Index: 1, Path: "pack/feature.mkv", Length: gib},
	}
	if junkFiles(bigSample)[0] {
		t.Error("skipped a sample that is half the torrent")
	}
}

func TestPreviewCancelDoesNotRemoveNonOwnedTorrent(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	h, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("first AddForPreview should own the torrent")
	}
	a := &App{
		eng:     eng,
		screen:  screenPreview,
		preview: newPreviewModel(h, magnet, "", screenSearch, false),
	}
	if _, cmd := a.updatePreview(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Fatal("esc should not return a command")
	}
	if a.screen != screenSearch {
		t.Fatalf("screen = %v, want search", a.screen)
	}
	if got := eng.Magnet(h); got == "" {
		t.Fatal("non-owned preview cancel removed an existing torrent")
	}
}

func TestPreviewExistingPausedTorrentDoesNotMutateIt(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	magnet := "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h, err := eng.Add(magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	eng.Pause(h)
	before, ok := eng.Snapshot(h)
	if !ok || before.State != engine.StatePaused {
		t.Fatalf("before preview = %+v, ok=%v; want paused", before, ok)
	}

	got, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if got != h || owned {
		t.Fatalf("AddForPreview = (%s,%v), want existing hash and owned=false", got.HexString(), owned)
	}
	after, ok := eng.Snapshot(h)
	if !ok {
		t.Fatal("tracked torrent disappeared")
	}
	if after.State != engine.StatePaused {
		t.Fatalf("preview mutated paused torrent: %+v", after)
	}
	if after.DownloadDir != before.DownloadDir || after.Seed != before.Seed {
		t.Fatalf("preview changed saved options: before=%+v after=%+v", before, after)
	}
}
