package filemove

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNeverReplacesDestination(t *testing.T) {
	root := t.TempDir()
	old, next := filepath.Join(root, "old"), filepath.Join(root, "next")
	for path, data := range map[string]string{old: "source", next: "destination"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Rename(old, next); err == nil {
		t.Fatal("overwrote an existing destination")
	}
	for path, want := range map[string]string{old: "source", next: "destination"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("collision changed %s: %v", path, err)
		}
	}
	if err := os.Remove(next); err != nil {
		t.Fatal(err)
	}
	if err := Rename(old, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("successful move left the source behind")
	}
}

func TestRenameNeverReplacesDirectory(t *testing.T) {
	root := t.TempDir()
	old, next := filepath.Join(root, "old"), filepath.Join(root, "next")
	for _, path := range []string{old, next} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := Rename(old, next); err == nil {
		t.Fatal("replaced an existing directory")
	}
}
