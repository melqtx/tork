//go:build linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeUserDirs(t *testing.T, configHome, contents string) {
	t.Helper()
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "user-dirs.dirs"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredDownloadDirExpandsHome(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(t.TempDir(), "config")
	writeUserDirs(t, configHome, `XDG_DOWNLOAD_DIR="$HOME/downloads"`+"\n")

	base, ok := configuredDownloadDir(home, configHome)
	if want := filepath.Join(home, "downloads"); !ok || base != want {
		t.Fatalf("configured download dir = %q, %v; want %q, true", base, ok, want)
	}

	// Exercise the production wrapper with the reported Linux setup: the
	// user-dirs entry exists but XDG_DOWNLOAD_DIR is not exported.
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DOWNLOAD_DIR", "")
	if got, want := defaultDownloadDir(), filepath.Join(home, "downloads", "tork"); got != want {
		t.Fatalf("default download dir = %q, want %q", got, want)
	}
}

func TestConfiguredDownloadDirHonorsConfigHome(t *testing.T) {
	home := t.TempDir()
	defaultConfig := filepath.Join(home, ".config")
	explicitConfig := filepath.Join(t.TempDir(), "xdg-config")
	writeUserDirs(t, defaultConfig, `XDG_DOWNLOAD_DIR="$HOME/default"`+"\n")
	writeUserDirs(t, explicitConfig, `XDG_DOWNLOAD_DIR="$HOME/explicit"`+"\n")

	got, ok := configuredDownloadDir(home, explicitConfig)
	if want := filepath.Join(home, "explicit"); !ok || got != want {
		t.Fatalf("configured download dir = %q, %v; want %q, true", got, ok, want)
	}
}

func TestConfiguredDownloadDirRejectsRelativeConfigHome(t *testing.T) {
	home := t.TempDir()
	writeUserDirs(t, filepath.Join(home, ".config"), `XDG_DOWNLOAD_DIR="${HOME}/default"`+"\n")

	got, ok := configuredDownloadDir(home, "relative/config")
	if want := filepath.Join(home, "default"); !ok || got != want {
		t.Fatalf("configured download dir = %q, %v; want %q, true", got, ok, want)
	}
}

func TestConfiguredDownloadDirAcceptsAbsoluteForms(t *testing.T) {
	home := t.TempDir()
	for _, quoted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unquoted", true: "quoted"}[quoted], func(t *testing.T) {
			configHome := filepath.Join(t.TempDir(), "config")
			want := filepath.Join(t.TempDir(), "downloads")
			value := want
			if quoted {
				value = `"` + value + `"`
			}
			writeUserDirs(t, configHome, "XDG_DOWNLOAD_DIR="+value+"\n")
			got, ok := configuredDownloadDir(home, configHome)
			if !ok || got != want {
				t.Fatalf("configured download dir = %q, %v; want %q, true", got, ok, want)
			}
		})
	}
}

func TestConfiguredDownloadDirPrecedesEnvironment(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(t.TempDir(), "config")
	writeUserDirs(t, configHome, `XDG_DOWNLOAD_DIR="$HOME/configured"`+"\n")
	envDir := filepath.Join(t.TempDir(), "environment")

	got := resolveDefaultDownloadDir(home, configHome, envDir)
	want := filepath.Join(home, "configured", "tork")
	if got != want {
		t.Fatalf("default download dir = %q, want config-precedence %q", got, want)
	}
}

func TestInvalidConfiguredDownloadDirFallsBackSafely(t *testing.T) {
	home := t.TempDir()
	envDir := filepath.Join(t.TempDir(), "safe-environment")
	values := map[string]string{
		"empty":                 `""`,
		"relative":              `"downloads"`,
		"unmatched quote":       `"$HOME/downloads`,
		"unsupported expansion": `"$TMPDIR/downloads"`,
		"command substitution":  `"$(touch /tmp/pwned)"`,
		"backticks":             "\"`touch /tmp/pwned`\"",
		"trailing shell":        `"$HOME/downloads"; touch /tmp/pwned`,
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			configHome := filepath.Join(t.TempDir(), "config")
			writeUserDirs(t, configHome, "XDG_DOWNLOAD_DIR="+value+"\n")
			got := resolveDefaultDownloadDir(home, configHome, envDir)
			if want := filepath.Join(envDir, "tork"); got != want {
				t.Fatalf("default download dir = %q, want safe fallback %q", got, want)
			}
		})
	}
}

func TestConfiguredDownloadDirDoesNotCreateTarget(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(t.TempDir(), "config")
	target := filepath.Join(home, "not-created")
	writeUserDirs(t, configHome, `XDG_DOWNLOAD_DIR="$HOME/not-created"`+"\n")

	if got, ok := configuredDownloadDir(home, configHome); !ok || got != target {
		t.Fatalf("configured download dir = %q, %v; want %q, true", got, ok, target)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("resolver created target: %v", err)
	}
}
