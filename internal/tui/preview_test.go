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
