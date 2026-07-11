package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/state"
)

func TestProxyURLFromArgsKeepsCredentialsOutOfCommandLine(t *testing.T) {
	raw, err := proxyURLFromArgs([]string{"socks5://127.0.0.1:1080"})
	if err != nil || raw != "socks5://127.0.0.1:1080" {
		t.Fatalf("generic URL = %q, %v", raw, err)
	}
	_, err = proxyURLFromArgs([]string{"socks5://alice:secret@127.0.0.1:1080"})
	if err == nil || !strings.Contains(err.Error(), "run 'tork proxy set'") {
		t.Fatalf("credentialed argument error = %v", err)
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credentialed argument error leaked URL data: %v", err)
	}
}

func TestNormalizeEntryPathsInfersLegacyDirWhenDataExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.dat")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := state.Entry{Name: "payload.dat"}
	normalizeEntryPaths(&e, dir)
	if e.DownloadDir != dir {
		t.Fatalf("DownloadDir = %q, want %q", e.DownloadDir, dir)
	}
	if e.DataPath != path {
		t.Fatalf("DataPath = %q, want file under default dir", e.DataPath)
	}
	if e.NeedsRelink || e.Paused {
		t.Fatalf("entry should be resumable: %+v", e)
	}
}

func TestNormalizeEntryPathsMarksLegacyMissing(t *testing.T) {
	dir := t.TempDir()
	e := state.Entry{Name: "payload.dat"}
	normalizeEntryPaths(&e, dir)
	if !e.NeedsRelink || !e.Paused {
		t.Fatalf("legacy entry without data should need relink and be paused: %+v", e)
	}
	if e.DownloadDir != "" {
		t.Fatalf("DownloadDir = %q, want empty so it cannot resume into a guessed folder", e.DownloadDir)
	}
	if e.DataPath != filepath.Join(dir, "payload.dat") {
		t.Fatalf("DataPath = %q, want display hint under legacy dir", e.DataPath)
	}
}

func TestEntryDataPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.dat")
	if entryDataPresent(state.Entry{DataPath: path}) {
		t.Fatal("missing file reported present")
	}
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !entryDataPresent(state.Entry{DataPath: path}) {
		t.Fatal("existing file reported missing")
	}
}

func TestEntryDataPresentAcceptsPartFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.dat")
	if err := os.WriteFile(path+".part", []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !entryDataPresent(state.Entry{DataPath: path}) {
		t.Fatal("part file reported missing")
	}
}
