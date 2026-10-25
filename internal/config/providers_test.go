package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSourceEditPreservesOldConfigAndDisabledChoices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# my setup
download_dir: /tmp/my-downloads # keep this
providers:
  yts:
    enabled: false # deliberately disabled
  x1337:
    enabled: false
    mirrors: [https://example.org]
  my_feed:
    enabled: true
    type: rss
    search_url: 'https://example.org/?apikey=keep-secret&q={query}'
custom_setting: keep-me
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.WriteProviderEnabled("x1337", true); err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["x1337"].Enabled {
		t.Fatal("disk writer changed in-memory choices before success was handled")
	}
	after, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Providers["x1337"].Enabled || after.Providers["yts"].Enabled || after.Providers["eztv"].Enabled || after.Providers["tpb_tv"].Enabled {
		t.Fatalf("source choices changed unexpectedly: %+v", after.Providers)
	}
	if after.Providers["x1337"].Mirrors[0] != "https://example.org" {
		t.Fatal("lost configured mirror")
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"# my setup", "# keep this", "# deliberately disabled", "custom_setting: keep-me", "apikey=keep-secret"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("edit lost %q", want)
		}
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("edit changed private file permissions")
	}
	// A builtin missing from an older config must be editable without rewriting
	// the other defaults into the user's file.
	if err := after.WriteProviderEnabled("eztv", true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadReadOnlyFrom(dir)
	if err != nil || !reloaded.Providers["eztv"].Enabled || reloaded.Providers["yts"].Enabled {
		t.Fatalf("old-config merge failed: %v", err)
	}
}

func TestSourceEditRefusesUnsafeOrInvalidConfig(t *testing.T) {
	for _, contents := range []string{
		"providers: [broken\n",
		"providers:\n  yts: {enabled: false, enabled: true}\n",
		"providers:\n  yts: {enabled: maybe}\n",
		"[]\n",
		"---\nproviders: {}\n---\nsecret: preserve\n",
		"shared: &shared {enabled: false}\nproviders:\n  yts: *shared\n",
		"providers:\n  yts: &shared {enabled: false}\n  nyaa: *shared\n",
		"providers:\n  yts:\n    enabled: &flag false\n  nyaa:\n    enabled: *flag\n",
		"shared: &shared {yts: {enabled: false}}\nproviders:\n  <<: *shared\n",
	} {
		t.Run(contents, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Default(dir).WriteProviderEnabled("yts", true); err == nil {
				t.Fatal("unsafe edit succeeded")
			}
			after, _ := os.ReadFile(path)
			if string(after) != contents {
				t.Fatal("failed edit modified original config")
			}
		})
	}
}

func TestSourceEditCreatesMissingConfigAndRejectsSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	cfg := Default(dir)
	if err := cfg.WriteProviderEnabled("eztv", true); err != nil {
		t.Fatal(err)
	}
	after, err := LoadReadOnlyFrom(dir)
	if err != nil || !after.Providers["eztv"].Enabled {
		t.Fatalf("first save: %v", err)
	}
	if err := cfg.WriteProviderEnabled("unknown", true); err == nil {
		t.Fatal("unknown source accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(t.TempDir(), "original")
	if err := os.Rename(cfg.ConfigPath(), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cfg.ConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := cfg.WriteProviderEnabled("yts", false); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestCustomSourceEditsPreserveCommentsAndReserveBuiltinNames(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(cfg.ConfigPath())
	if err := os.WriteFile(cfg.ConfigPath(), append([]byte("# keep this comment\n"), original...), 0o644); err != nil {
		t.Fatal(err)
	}
	p := ProviderConfig{Label: "Linux feed", Type: "torznab", SearchURL: "https://example.org/api", APIKey: "secret", Enabled: true}
	if err := cfg.WriteCustomSource("linux-feed", p, false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.WriteCustomSource("linux-feed", p, false); err == nil {
		t.Fatal("duplicate source overwritten")
	}
	p.Label = "Renamed"
	p.APIKey = ""
	if err := cfg.WriteCustomSource("linux-feed", p, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReadOnlyFrom(dir)
	if err != nil || loaded.Providers["linux-feed"].Label != "Renamed" || loaded.Providers["linux-feed"].APIKey != "" {
		t.Fatal("source edit did not persist")
	}
	for _, id := range []string{"nyaa", "x1337", "1337x", "tpb-movies"} {
		if err := cfg.WriteCustomSource(id, p, false); err == nil {
			t.Fatal("reserved identity was not protected")
		}
		if err := cfg.RemoveCustomSource(id); err == nil {
			t.Fatal("builtin source was removed")
		}
	}
	if err := cfg.RemoveCustomSource("linux-feed"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	if !strings.Contains(string(data), "# keep this comment") || strings.Contains(string(data), "secret") {
		t.Fatal("custom edit lost comments or retained removed credentials")
	}
}

func TestCustomSourceRemovalRefusesNestedAnchors(t *testing.T) {
	dir := t.TempDir()
	contents := "providers:\n  custom:\n    type: torznab\n    search_url: https://example.org/api\n    enabled: &enabled false\n  other:\n    enabled: *enabled\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RemoveCustomSource("custom"); err == nil {
		t.Fatal("removal stranded an alias")
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	if string(data) != contents {
		t.Fatal("failed removal modified config")
	}
}
