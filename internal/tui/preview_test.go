package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
)

func TestDetectMagnetInput(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		kind     magnetInputKind
		name     string
		contains string
	}{
		{"magnet:?xt=urn:btih:ABCDEF&dn=My+File", true, magnetInputMagnet, "My File", "magnet:?"},
		{"0123456789abcdef0123456789abcdef01234567", true, magnetInputInfoHash, "", "magnet:?xt=urn:btih:"},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", true, magnetInputInfoHash, "", "magnet:?xt=urn:btih:"},
		{"https://example.test/path/My%20File.torrent", true, magnetInputTorrentURL, "My File.torrent", "https://"},
		{"dune 2024", false, 0, "", ""},
	}
	for _, c := range cases {
		target, name, kind, ok := detectMagnetInput(c.in)
		if ok != c.ok || kind != c.kind || name != c.name {
			t.Errorf("detectMagnetInput(%q) = (%q,%q,%v,%v), want ok=%v kind=%v name=%q", c.in, target, name, kind, ok, c.ok, c.kind, c.name)
		}
		if c.contains != "" && !strings.Contains(target, c.contains) {
			t.Errorf("target %q does not contain %q", target, c.contains)
		}
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
