//go:build integration

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestResumeAllKeepsCategoryDestinationAfterSettingsChange(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(t.TempDir(), "Linux")
	newDir := t.TempDir()
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("previously downloaded ISO bytes")
	path := filepath.Join(oldDir, "image.iso")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	saved := &state.State{Entries: []state.Entry{{
		Magnet: "https://example.invalid/image.iso", Name: "image.iso", Category: "linux",
		DownloadDir: oldDir, DataPath: path, AddedAt: time.Now().UTC(),
		SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Seed: state.Bool(false),
	}}}
	if err := saved.Save(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(
		"download_dir: %q\norganization:\n  enabled: false\n  folders:\n    linux: Distros\n", newDir,
	)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load(cfg.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	resumeAll(eng, loaded, cfg, newDir)
	snaps := eng.Snapshots()
	if len(snaps) != 1 || snaps[0].DownloadDir != oldDir || snaps[0].DataPath != path {
		t.Fatalf("startup changed the saved destination: %+v", snaps)
	}
	if snaps[0].State != engine.StateDone || loaded.Entries[0].Category != "linux" {
		t.Fatalf("startup lost the completed data or category: %+v", snaps[0])
	}
	files, err := os.ReadDir(newDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("startup wrote into the new download root: %v", err)
	}
}
