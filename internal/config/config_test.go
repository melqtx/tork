package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLoadFromConfiguresAndSecuresSOCKS5Credentials(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("proxy:\n  socks5: socks5://alice:secret@127.0.0.1:9050\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ProxyRuntime().Enabled() || cfg.ProxyRuntime().Endpoint() != "127.0.0.1:9050" {
		t.Fatalf("proxy runtime = %+v, want enabled redacted endpoint", cfg.ProxyRuntime())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %#o, want 0600", got)
	}
}

func TestLoadReadOnlyProxyCredentialsDoNotMutatePermissions(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	data := []byte("proxy:\n  socks5: socks5://alice:secret@127.0.0.1:9050\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	secure, err := cfg.ProxyCredentialConfigSecure()
	if err != nil {
		t.Fatal(err)
	}
	if secure {
		t.Fatal("read-only load treated mode 0644 credential config as secure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatal("read-only load changed config contents")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("read-only load changed config mode to %#o", got)
	}
}

func TestLoadReadOnlyReportsInvalidSOCKS5AndBlocksHTTP(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("proxy:\n  socks5: http://alice:secret@127.0.0.1:9050\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProxyError() == nil {
		t.Fatal("invalid proxy config was not reported")
	}
	if strings.Contains(cfg.ProxyError().Error(), "secret") || strings.Contains(cfg.ProxyError().Error(), "alice") {
		t.Fatalf("proxy error leaked credentials: %v", cfg.ProxyError())
	}
	if cfg.ProxyRuntime().Enabled() {
		t.Fatal("invalid proxy config left a usable runtime")
	}
	client := cfg.ProxyHTTPClient()
	if client == nil {
		t.Fatal("invalid proxy config returned a direct HTTP client")
	}
	if _, err := client.Get("https://example.invalid"); err == nil || !strings.Contains(err.Error(), "request blocked") {
		t.Fatalf("invalid proxy request = %v, want a local fail-closed error", err)
	}
}

func TestLoadRejectsCredentialBearingConfigSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not a supported credential-store contract on Windows")
	}
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	dir := filepath.Join(t.TempDir(), ".tork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "config-target.yaml")
	if err := os.WriteFile(target, []byte("proxy:\n  socks5: socks5://alice:secret@127.0.0.1:9050\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("LoadFrom(symlink config) = %v, want regular-file refusal", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("credential target mode = %#o, want unchanged 0644", got)
	}
}

func TestUpdateProxyPreservesCommentsAndUnrelatedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("# keep this comment\nfuture_option: stay-put\nproxy:\n  # keep this too\n  socks5: socks5://127.0.0.1:1080\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	update, err := UpdateProxyFrom(dir, "socks5://127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Changed || update.Endpoint != "127.0.0.1:9050" {
		t.Fatalf("update = %+v", update)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"# keep this comment", "# keep this too", "future_option: stay-put", "socks5://127.0.0.1:9050"} {
		if !strings.Contains(text, want) {
			t.Fatalf("updated config lost %q:\n%s", want, text)
		}
	}

	update, err = UpdateProxyFrom(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Changed || update.Enabled {
		t.Fatalf("off update = %+v", update)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "socks5:") || !strings.Contains(string(got), "future_option: stay-put") {
		t.Fatalf("proxy off rewrote unrelated config:\n%s", got)
	}
}

func TestUpdateProxyCreatesAndSecuresCredentialConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	update, err := UpdateProxyFrom(dir, "socks5://alice:secret@127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Changed || !update.Enabled || update.Endpoint != "127.0.0.1:9050" {
		t.Fatalf("update = %+v", update)
	}
	info, err := os.Stat(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %#o, want 0600", got)
	}
}

func TestUpdateProxyHardensExistingCredentialConfigEvenWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := "socks5://alice:secret@127.0.0.1:9050"
	if err := os.WriteFile(path, []byte("proxy:\n  socks5: "+raw+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	update, err := UpdateProxyFrom(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !update.Changed {
		t.Fatal("credential hardening was reported as a no-op")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %#o, want 0600", got)
	}
}

func TestUpdateProxyInvalidInputAndOffDoNotCreateOrRewrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tork")
	if update, err := UpdateProxyFrom(dir, ""); err != nil || update.Changed {
		t.Fatalf("off missing config = %+v, %v", update, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("off created %s: %v", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	before := []byte("future_option: keep\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateProxyFrom(dir, "http://alice:secret@127.0.0.1:9050")
	if err == nil {
		t.Fatal("invalid proxy scheme was accepted")
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid proxy error leaked credentials: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("invalid update changed config: %q (%v)", after, err)
	}
}

func TestUpdateProxyRefusesMalformedConfigWithoutLeakingOrRewriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	before := []byte("proxy: [socks5://alice:secret@127.0.0.1:9050\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateProxyFrom(dir, "socks5://127.0.0.1:9050")
	if err == nil || !strings.Contains(err.Error(), "invalid YAML") {
		t.Fatalf("malformed config update error = %v", err)
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("malformed config error leaked credentials: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("malformed update changed config: %q (%v)", after, err)
	}
}

func TestUpdateProxyRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink editing contract is Unix-only")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	before := []byte("future_option: keep\n")
	if err := os.WriteFile(target, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateProxyFrom(dir, "socks5://127.0.0.1:9050")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink update error = %v", err)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(before) {
		t.Fatalf("symlink target changed: %q (%v)", after, err)
	}
}
