package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromWritesDefaultsOnFirstRun(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.ConfigPath()); err != nil {
		t.Errorf("default config file not written: %v", err)
	}
	if _, err := os.Stat(cfg.DownloadDir); err != nil {
		t.Errorf("download dir not created: %v", err)
	}
	if !cfg.SeedAfterComplete || cfg.MaxConnections != 50 || cfg.SearchTimeoutSeconds != 15 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.Providers) != 7 {
		t.Errorf("expected 7 providers, got %d", len(cfg.Providers))
	}
	if !cfg.Providers["knaben"].Enabled || !cfg.Providers["yts"].Enabled || !cfg.Providers["nyaa"].Enabled {
		t.Error("knaben/yts/nyaa should be enabled by default")
	}
	if cfg.Providers["tpb_movies"].Enabled || cfg.Providers["tpb_tv"].Enabled {
		t.Error("apibay Pirate Bay providers should be opt-in (unreliable) by default")
	}
	if cfg.Providers["eztv"].Enabled || cfg.Providers["x1337"].Enabled {
		t.Error("html scraper providers should be opt-in by default")
	}
	if cfg.Health.Enabled || cfg.Health.IntervalHours != 24 {
		t.Errorf("health defaults = %+v, want disabled daily checks", cfg.Health)
	}
}

func TestLoadReadOnlyDoesNotCreateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Health.Enabled {
		t.Fatal("read-only defaults must leave automatic health checks disabled")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only load created %s: %v", dir, err)
	}
}

func TestLoadReadOnlyPreservesHealthOptIn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("health:\n  enabled: true\n  interval_hours: 12\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Health.Enabled || cfg.Health.IntervalHours != 12 {
		t.Errorf("health opt-in = %+v, want enabled every 12h", cfg.Health)
	}
}

func TestLoadReadOnlyMergesProviderDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("providers:\n  yts: {enabled: false}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["yts"].Enabled || !cfg.Providers["knaben"].Enabled {
		t.Errorf("read-only provider defaults = %+v", cfg.Providers)
	}
}

func TestLoadFromReadsExistingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "download_dir: " + filepath.Join(dir, "dl") + "\nmax_connections: 7\nproviders:\n  yts: {enabled: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConnections != 7 {
		t.Errorf("MaxConnections = %d, want 7", cfg.MaxConnections)
	}
	if cfg.Providers["yts"].Enabled {
		t.Error("yts should be disabled")
	}
	// providers absent from the file are merged from defaults
	if _, ok := cfg.Providers["knaben"]; !ok || !cfg.Providers["knaben"].Enabled {
		t.Errorf("missing provider defaults not merged correctly: %+v", cfg.Providers)
	}
	if cfg.Providers["eztv"].Enabled {
		t.Error("eztv should remain opt-in after merge")
	}
}

func TestOverrideDownloadDir(t *testing.T) {
	cfg := Default(t.TempDir())
	target := filepath.Join(t.TempDir(), "custom", "dl")
	if err := cfg.OverrideDownloadDir(target); err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadDir != target {
		t.Errorf("DownloadDir = %q, want %q", cfg.DownloadDir, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("override dir not created: %v", err)
	}
}

func TestProviderBaseURLs(t *testing.T) {
	p := ProviderConfig{Mirror: "https://a", Mirrors: []string{"https://b", "https://c"}}
	got := p.BaseURLs()
	if len(got) != 3 || got[0] != "https://a" || got[2] != "https://c" {
		t.Errorf("BaseURLs = %v", got)
	}
}

func TestLoadFromRecoversFromCorruptConfig(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "download_dir: [this is: not valid yaml\n  providers: {{{\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir) // must NOT error
	if err != nil {
		t.Fatalf("corrupt config should not fail startup: %v", err)
	}
	if cfg.MaxConnections != 50 || len(cfg.Providers) != 7 {
		t.Errorf("did not fall back to defaults: %+v", cfg)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("corrupt config should be preserved as .bak: %v", err)
	}
}
