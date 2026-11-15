//go:build integration

package tui

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/metacache"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

func TestOrganizationPreviewAndQuickDownloadWriteToChosenFolder(t *testing.T) {
	cases := []struct {
		name     string
		preview  bool
		resolved bool
	}{
		{name: "preview", preview: true},
		{name: "quick download"},
		{name: "resolved detail page", preview: true, resolved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, res, payload := organizedTorrentApp(t)
			cmd := a.actOnResult(res, tc.preview)
			if tc.resolved {
				listing := res
				listing.Magnet = ""
				_, cmd = a.updateResults(magnetResolvedMsg{res: listing, magnet: res.Magnet, preview: true})
			}
			if cmd == nil {
				t.Fatal("missing add command")
			}
			msg := cmd()
			wantDir := filepath.Join(a.cfg.DownloadDir, "Movies")
			if tc.preview {
				ready, ok := msg.(previewReadyMsg)
				if !ok || ready.err != nil {
					t.Fatalf("preview result = %+v", msg)
				}
				a.Update(ready)
				if a.previewDestination() != wantDir || a.preview.category != "movies" {
					t.Fatalf("preview destination = %q, category = %q", a.previewDestination(), a.preview.category)
				}
				a.cfg.Organization.Enabled = false
				a.startPreviewDownload()
			} else {
				added, ok := msg.(torrentAddedMsg)
				if !ok || added.err != nil {
					t.Fatalf("add result = %+v", msg)
				}
				a.Update(added)
			}
			if len(a.st.Entries) != 1 {
				t.Fatalf("entries = %+v", a.st.Entries)
			}
			entry := a.st.Entries[0]
			if entry.Category != "movies" || entry.DownloadDir != wantDir {
				t.Fatalf("saved destination = %+v", entry)
			}
			wantPath := filepath.Join(wantDir, "payload.dat")
			deadline := time.Now().Add(10 * time.Second)
			for {
				data, err := os.ReadFile(wantPath)
				if err == nil && bytes.Equal(data, payload) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("payload did not arrive in %s: %v; snapshots: %+v", wantPath, err, a.eng.Snapshots())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(filepath.Join(a.cfg.DownloadDir, "payload.dat")); !os.IsNotExist(err) {
				t.Fatalf("payload also appeared in the root folder: %v", err)
			}
			if err := a.CheckpointSession(); err != nil {
				t.Fatal(err)
			}
			loaded, err := state.Load(a.cfg.StatePath())
			if err != nil || len(loaded.Entries) != 1 || loaded.Entries[0].Category != "movies" {
				t.Fatalf("persisted state = %+v, err = %v", loaded, err)
			}
			snap := a.eng.Snapshots()[0]
			if err := a.eng.Pause(snap.Hash); err != nil {
				t.Fatal(err)
			}
			a.st.Entries[0].Paused = true
			a.cfg.Organization.Enabled = false
			res.Magnet += "&dn=another+listing"
			a.Update(a.actOnResult(res, false)())
			if len(a.st.Entries) != 1 || a.st.Entries[0].AddedAt != entry.AddedAt {
				t.Fatalf("duplicate replaced state: %+v", a.st.Entries)
			}
			if snap, ok := a.eng.Snapshot(snap.Hash); !ok || snap.DownloadDir != wantDir {
				t.Fatalf("duplicate moved storage: %+v, ok = %v", snap, ok)
			}
			if a.st.Entries[0].Category != "movies" {
				t.Fatal("duplicate erased the original category")
			}
			if a.st.Entries[0].Paused {
				t.Fatal("resumed duplicate remained paused in saved state")
			}
			if err := a.eng.Remove(snap.Hash, false); err != nil {
				t.Fatal(err)
			}
			a.cfg.Organization.Enabled = true
			a.cfg.Organization.Folders = map[string]string{"movies": "Films"}
			a.Update(a.actOnResult(res, false)())
			if len(a.st.Entries) != 1 || a.st.Entries[0].DownloadDir != wantDir {
				t.Fatalf("history re-add changed destination: %+v", a.st.Entries)
			}
			if _, err := os.Stat(filepath.Join(a.cfg.DownloadDir, "Films")); !os.IsNotExist(err) {
				t.Fatalf("history re-add created an unused category folder: %v", err)
			}
		})
	}
}

func organizedTorrentApp(t *testing.T) (*App, provider.Result, []byte) {
	t.Helper()
	dir, downloads := t.TempDir(), t.TempDir()
	configYAML := fmt.Sprintf("download_dir: %q\nseed_after_complete: false\norganization: {enabled: true}\n", downloads)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("a useful download\n"), 2048)
	sourcePath := filepath.Join(t.TempDir(), "payload.dat")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(sourcePath); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "payload.dat", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(srv.Close)
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, UrlList: metainfo.UrlList{srv.URL + "/payload.dat"}}
	if err := metacache.New(cfg).Store(mi); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	hash := mi.HashInfoBytes()
	res := provider.Result{
		Title: "Useful release", Magnet: mi.Magnet(&hash, &info).String(), Category: "Movies/HD",
	}
	a := New(cfg, eng, nil, &state.State{}, nil)
	a.screen, a.width, a.height = screenResults, 100, 30
	return a, res, payload
}

func TestPreviewCategoryPickerChangesActualStorage(t *testing.T) {
	a, res, payload := organizedTorrentApp(t)
	a.Update(a.actOnResult(res, true)())
	a.preview.refresh(a.eng)
	a.preview.excluded[0] = true
	a.openPreviewCategories()
	for i, option := range a.preview.categories.options {
		if option.ID == "tv" {
			a.preview.categories.win.cursor = i
		}
	}
	_, cmd := a.updatePreviewCategories(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !a.preview.changingCategory {
		t.Fatal("category change did not start")
	}
	a.Update(cmd())
	if a.preview.categories != nil || !a.preview.excluded[0] || a.preview.category != "tv" {
		t.Fatalf("picker=%v excluded=%v category=%q error=%q",
			a.preview.categories, a.preview.excluded[0], a.preview.category, a.errText)
	}
	want := filepath.Join(a.cfg.DownloadDir, "TV", "payload.dat")
	delete(a.preview.excluded, 0)
	a.startPreviewDownload()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(want)
		if err == nil && bytes.Equal(data, payload) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("payload did not reach the chosen folder: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.eng.SetPreviewDownloadDir(a.preview.hash, a.cfg.DownloadDir); err == nil {
		t.Fatal("running download accepted a preview-only folder change")
	}
}
