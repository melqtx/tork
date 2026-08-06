//go:build !linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNonLinuxDefaultIgnoresUserDirsFile(t *testing.T) {
	home := t.TempDir()
	configHome := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(configHome, "user-dirs.dirs"),
		[]byte(`XDG_DOWNLOAD_DIR="$HOME/from-config"`+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	envDir := filepath.Join(t.TempDir(), "from-environment")
	got := resolveDefaultDownloadDir(home, configHome, envDir)
	if want := filepath.Join(envDir, "tork"); got != want {
		t.Fatalf("default download dir = %q, want %q", got, want)
	}
}
