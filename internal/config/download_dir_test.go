package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultDownloadDirFallsBackToEnvironment(t *testing.T) {
	home := t.TempDir()
	envDir := filepath.Join(t.TempDir(), "from-env")
	got := resolveDefaultDownloadDir(home, filepath.Join(t.TempDir(), "missing-config"), envDir)
	want := filepath.Join(envDir, "tork")
	if got != want {
		t.Fatalf("default download dir = %q, want %q", got, want)
	}
}

func TestDefaultDownloadDirFallsBackToHomeDownloads(t *testing.T) {
	home := t.TempDir()
	got := resolveDefaultDownloadDir(home, filepath.Join(t.TempDir(), "missing-config"), "")
	want := filepath.Join(home, "Downloads", "tork")
	if got != want {
		t.Fatalf("default download dir = %q, want %q", got, want)
	}
}

func TestNormalizeDownloadDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home", "person")
	absolute := filepath.Join(t.TempDir(), "downloads")
	tests := []struct {
		name       string
		raw        string
		allowTilde bool
		want       string
		ok         bool
	}{
		{name: "absolute", raw: absolute, want: absolute, ok: true},
		{name: "literal punctuation", raw: absolute + " (person's)", want: absolute + " (person's)", ok: true},
		{name: "dollar home", raw: "$HOME/downloads", want: filepath.Join(home, "downloads"), ok: true},
		{name: "braced home", raw: "${HOME}/downloads", want: filepath.Join(home, "downloads"), ok: true},
		{name: "tilde compatibility", raw: "~/downloads", allowTilde: true, want: filepath.Join(home, "downloads"), ok: true},
		{name: "tilde disabled", raw: "~/downloads"},
		{name: "empty", raw: ""},
		{name: "relative", raw: "downloads"},
		{name: "unsupported variable", raw: "$TMPDIR/downloads"},
		{name: "command substitution", raw: "$(touch /tmp/pwned)"},
		{name: "backticks", raw: "`touch /tmp/pwned`"},
		{name: "shell operator", raw: absolute + ";touch /tmp/pwned"},
		{name: "quoted shell fragment", raw: `"` + absolute + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeDownloadDir(tt.raw, home, tt.allowTilde)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("normalizeDownloadDir(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}
