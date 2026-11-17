package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRestoresLastGoodState(t *testing.T) {
	for _, damage := range []string{"broken JSON", "null", "missing"} {
		t.Run(damage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			s := &State{Entries: []Entry{{Magnet: "saved", Name: "original", Category: "movies",
				Paused: true, Seed: Bool(false), Excluded: []int{2}}}}
			if err := s.Save(path); err != nil {
				t.Fatal(err)
			}
			s.Entries[0].Name = "updated"
			if err := s.Save(path); err != nil {
				t.Fatal(err)
			}
			if damage == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(damage), 0o600); err != nil {
				t.Fatal(err)
			}
			recovered, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			e := recovered.Entries[0]
			if e.Name != "original" || e.Category != "movies" || !e.Paused || *e.Seed || len(e.Excluded) != 1 {
				t.Fatalf("recovery lost download settings: %+v", e)
			}
			primary, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(path + ".lastgood")
			if err != nil || !bytes.Equal(primary, backup) {
				t.Fatalf("recovery did not restore the primary file: %v", err)
			}
		})
	}
}

func TestInvalidBackupCannotResetDownloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, suffix := range []string{"", ".lastgood"} {
		if err := os.WriteFile(path+suffix, []byte("broken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(path); err == nil {
		t.Fatal("two corrupt snapshots started an empty queue")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "broken" {
		t.Fatalf("failed recovery rewrote the primary file: %v", err)
	}
}

func TestBackupRecoveryFollowsAnInterruptedMove(t *testing.T) {
	root := t.TempDir()
	old, next := filepath.Join(root, "old", "payload"), filepath.Join(root, "movies", "payload")
	for _, path := range []string{old, next} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(old, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &State{Entries: []Entry{{Magnet: "saved", Name: "payload", DownloadDir: filepath.Dir(old),
		DataPath: old, BytesCompleted: 7, Paused: true, PendingMove: &MoveTarget{
			DownloadDir: filepath.Dir(next), DataPath: next, Category: "movies", CategorySource: "manual"}}}}
	path := filepath.Join(root, "state.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, next); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Entries[0].DataPath != next || got.Entries[0].Category != "movies" || got.Entries[0].PendingMove != nil {
		t.Fatalf("backup restored the obsolete location: %+v, %v", got, err)
	}
}
