package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStateBackupPreservesPreviousValidSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{Entries: []Entry{{Magnet: "saved", Name: "original", Category: "movies", Paused: true}}}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Entries[0].Name = "updated"
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".lastgood")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("previous snapshot lost: %v", err)
	}
	if err := os.WriteFile(path, []byte("broken JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	backup, err = os.ReadFile(path + ".lastgood")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("corrupt input replaced the good backup: %v", err)
	}
}

func TestStateSaveReadFailurePreservesBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{Entries: []Entry{{Magnet: "saved", Name: "original"}}}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".lastgood")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	s.Entries[0].Name = "updated"
	if err := s.Save(path); err == nil {
		t.Fatal("save ignored an unreadable primary state")
	}
	got, err := os.ReadFile(path + ".lastgood")
	if err != nil || !bytes.Equal(got, backup) {
		t.Fatalf("failed save damaged the backup: %v", err)
	}
}

func TestMoveIntentIsBackedUpBeforeFilesMove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{Entries: []Entry{{Magnet: "saved", Name: "payload"}}}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s.Entries[0].PendingMove = &MoveTarget{DownloadDir: t.TempDir(), Category: "movies"}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".lastgood")
	if err != nil {
		t.Fatal(err)
	}
	var backup State
	if err := json.Unmarshal(data, &backup); err != nil || backup.Entries[0].PendingMove == nil {
		t.Fatalf("backup lost the active move's recovery record: %v", err)
	}
}
