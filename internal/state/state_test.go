package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFile(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty state, got %d entries", len(s.Entries))
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{}
	s.Upsert(Entry{Magnet: "magnet:?xt=urn:btih:abc", Name: "test", AddedAt: time.Now().UTC()})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "test" {
		t.Errorf("round trip mismatch: %+v", got.Entries)
	}
}

func TestUpsertReplacesAndRemove(t *testing.T) {
	s := &State{}
	s.Upsert(Entry{Magnet: "m1", Name: "a"})
	s.Upsert(Entry{Magnet: "m1", Name: "b", Done: true})
	if len(s.Entries) != 1 || s.Entries[0].Name != "b" || !s.Entries[0].Done {
		t.Errorf("upsert did not replace: %+v", s.Entries)
	}
	if e := s.Find("m1"); e == nil || e.Name != "b" {
		t.Errorf("Find failed: %+v", e)
	}
	s.Remove("m1")
	if len(s.Entries) != 0 {
		t.Error("remove failed")
	}
}

func TestLoadRecoversFromCorruptState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path) // must NOT error
	if err != nil {
		t.Fatalf("corrupt state should not fail startup: %v", err)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty state, got %d entries", len(s.Entries))
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("corrupt state should be preserved as .bak: %v", err)
	}
}
