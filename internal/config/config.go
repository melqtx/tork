// Package config owns ~/.tork paths and the YAML config file.
package config

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/melqtx/tork/internal/proxy"
	"github.com/melqtx/tork/internal/rank"
)

type ProviderConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Type      string   `yaml:"type,omitempty"`
	Mirror    string   `yaml:"mirror,omitempty"`
	Mirrors   []string `yaml:"mirrors,omitempty"`
	SearchURL string   `yaml:"search_url,omitempty"`
}

// BaseURLs returns all base URLs for the provider, mirror first.
func (p ProviderConfig) BaseURLs() []string {
	var urls []string
	if p.Mirror != "" {
		urls = append(urls, p.Mirror)
	}
	urls = append(urls, p.Mirrors...)
	return urls
}

type AutopilotConfig struct {
	MaxDownloads      int      `yaml:"max_downloads"`
	MinSeeders        int      `yaml:"min_seeders"`
	MaxSizeGB         float64  `yaml:"max_size_gb,omitempty"`
	AllowedCategories []string `yaml:"allowed_categories,omitempty"`
}

// HealthConfig tunes the opportunistic health check that runs on launch when
// the last recorded check is older than the interval.
type HealthConfig struct {
	Enabled       bool `yaml:"enabled"`
	IntervalHours int  `yaml:"interval_hours"`
}

// ProxyConfig controls tork's optional strict SOCKS5 route. A configured proxy
// is deliberately global: searches and downloads share the same privacy path.
type ProxyConfig struct {
	SOCKS5 string `yaml:"socks5,omitempty"`
}

type MetadataCacheConfig struct {
	Enabled    bool `yaml:"enabled"`
	MaxMB      int  `yaml:"max_mb"`
	MaxEntries int  `yaml:"max_entries"`
}

// Interval is the gap between automatic health checks. A missing or nonsensical
// interval_hours falls back to the daily default rather than turning every
// launch into a provider probe.
func (h HealthConfig) Interval() time.Duration {
	if h.IntervalHours < 1 {
		return 24 * time.Hour
	}
	return time.Duration(h.IntervalHours) * time.Hour
}

type Config struct {
	DownloadDir           string                    `yaml:"download_dir"`
	SeedAfterComplete     bool                      `yaml:"seed_after_complete"`
	MaxConnections        int                       `yaml:"max_connections"`
	ListenPort            int                       `yaml:"listen_port"`
	SearchTimeoutSeconds  int                       `yaml:"search_timeout_seconds"`
	PreviewBeforeDownload bool                      `yaml:"preview_before_download"`
	HideNSFW              bool                      `yaml:"hide_nsfw"`
	Ranking               rank.Weights              `yaml:"ranking"`
	Autopilot             AutopilotConfig           `yaml:"autopilot"`
	Health                HealthConfig              `yaml:"health"`
	Proxy                 ProxyConfig               `yaml:"proxy"`
	MetadataCache         MetadataCacheConfig       `yaml:"metadata_cache"`
	Providers             map[string]ProviderConfig `yaml:"providers"`

	dir          string // ~/.tork, resolved at load time
	proxyRuntime *proxy.Runtime
	proxyErr     error // read-only loads report a bad proxy URL instead of failing
}

func (c *Config) SearchTimeout() time.Duration {
	return time.Duration(c.SearchTimeoutSeconds) * time.Second
}

// OverrideDownloadDir points downloads at dir (expanding a leading ~) and
// ensures it exists. Used by the --download-dir flag.
func (c *Config) OverrideDownloadDir(dir string) error {
	c.DownloadDir = expandHome(dir)
	return os.MkdirAll(c.DownloadDir, 0o755)
}

// SetDownloadDir points downloads at dir without creating it, so a read-only
// caller (doctor) can inspect a directory rather than conjure it into being.
func (c *Config) SetDownloadDir(dir string) {
	c.DownloadDir = expandHome(dir)
}

func (c *Config) Dir() string                  { return c.dir }
func (c *Config) StatePath() string            { return filepath.Join(c.dir, "state.json") }
func (c *Config) ConfigPath() string           { return filepath.Join(c.dir, "config.yaml") }
func (c *Config) HealthPath() string           { return filepath.Join(c.dir, "health.json") }
func (c *Config) AutopilotHistoryPath() string { return filepath.Join(c.dir, "autopilot.jsonl") }
func (c *Config) MetadataCacheDir() string     { return filepath.Join(c.dir, "metainfo") }
func (c *Config) PieceCompletionDir() string   { return c.dir }
func (c *Config) ProxyRuntime() *proxy.Runtime { return c.proxyRuntime }

// ProxyError is the proxy misconfiguration a read-only load tolerated so that
// diagnostics can describe it. It is always nil after Load/LoadFrom, which
// refuse to start on a bad proxy rather than ever running direct.
func (c *Config) ProxyError() error { return c.proxyErr }

// ProxyHTTPClient is the shared catalog-fetch client for provider searches
// and ISO resolution while the proxy is enabled; nil means the caller should
// keep its normal default client. It mirrors those defaults - a bounded
// overall timeout and a redirect cap - so proxy mode changes the route, not
// the fetch contract.
func (c *Config) ProxyHTTPClient() *http.Client {
	if c == nil {
		return nil
	}
	if c.proxyErr != nil || (strings.TrimSpace(c.Proxy.SOCKS5) != "" && c.proxyRuntime == nil) {
		return &http.Client{Transport: proxyConfigErrorTransport{}}
	}
	if c.proxyRuntime == nil || !c.proxyRuntime.Enabled() {
		return nil
	}
	client := c.proxyRuntime.HTTPClient(25*time.Second, 0)
	client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("stopped after 6 redirects")
		}
		return nil
	}
	return client
}

type proxyConfigErrorTransport struct{}

func (proxyConfigErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("proxy config is invalid; request blocked")
}

// ProxyCredentialConfigSecure reports whether a credential-bearing config is
// private to its owner. It never changes the file, so doctor can call it.
func (c *Config) ProxyCredentialConfigSecure() (bool, error) {
	if c.proxyRuntime == nil || !c.proxyRuntime.HasCredentials() {
		return true, nil
	}
	info, err := os.Lstat(c.ConfigPath())
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s must be a regular file when it holds proxy credentials", c.ConfigPath())
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", c.ConfigPath())
	}
	if goruntime.GOOS == "windows" {
		// Windows file modes cannot express owner-only access: Perm() reports
		// 0666 for any writable file and Chmod can only toggle read-only, so
		// the 0600 contract would reject every credentialed config forever.
		// Privacy of %USERPROFILE% is left to its ACLs.
		return true, nil
	}
	return info.Mode().Perm()&0o077 == 0, nil
}

func Default(dir string) *Config {
	runtime, _ := proxy.New("")
	return &Config{
		DownloadDir:           defaultDownloadDir(),
		SeedAfterComplete:     true,
		MaxConnections:        50,
		ListenPort:            0,
		SearchTimeoutSeconds:  15,
		PreviewBeforeDownload: true,
		HideNSFW:              true,
		Ranking:               rank.DefaultWeights(),
		Autopilot:             AutopilotConfig{MaxDownloads: 10, MinSeeders: 5},
		Health:                HealthConfig{Enabled: false, IntervalHours: 24},
		MetadataCache:         MetadataCacheConfig{Enabled: true, MaxMB: 256, MaxEntries: 512},
		Providers: map[string]ProviderConfig{
			"knaben": {Enabled: true, Type: "knaben", Mirror: "https://knaben.org"},
			"yts":    {Enabled: true, Type: "yts", Mirror: "https://yts.mx"},
			"nyaa":   {Enabled: true, Type: "nyaa", Mirror: "https://nyaa.si"},
			// apibay.org is frequently slow/unreachable; Knaben already
			// aggregates The Pirate Bay, so these are opt-in.
			"tpb_movies": {Enabled: false, Type: "tpb_movies", Mirror: "https://apibay.org"},
			"tpb_tv":     {Enabled: false, Type: "tpb_tv", Mirror: "https://apibay.org"},
			"eztv":       {Enabled: false, Type: "eztv_html", Mirror: "https://eztv.re"},
			"x1337":      {Enabled: false, Type: "1337x_html", Mirrors: []string{"https://1337x.to", "https://1337x.st", "https://x1337x.ws"}},
		},
		dir:          dir,
		proxyRuntime: runtime,
	}
}

// Load reads ~/.tork/config.yaml, writing the default file on first run.
// Missing keys fall back to defaults. It also ensures the directories exist.
func Load() (*Config, error) {
	dir, err := UserDir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// LoadReadOnly reads the user's configuration without creating, repairing, or
// otherwise touching any files or directories. Diagnostics use this path so a
// check can never change the machine it is inspecting.
func LoadReadOnly() (*Config, error) {
	dir, err := UserDir()
	if err != nil {
		return nil, err
	}
	return LoadReadOnlyFrom(dir)
}

// UserDir returns tork's per-user state directory without creating it. It is
// shared by the normal loaders and explicit configuration commands so a
// read-only command can always resolve the same path without side effects.
func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tork"), nil
}

// ProxyUpdate describes an attempted proxy-config change. Endpoint is always
// redacted and safe to print.
type ProxyUpdate struct {
	Changed  bool
	Enabled  bool
	Endpoint string
}

// UpdateProxy updates only proxy.socks5 in the user's config. It validates the
// route before touching disk, preserves unrelated YAML nodes and comments, and
// replaces the file atomically. Empty raw disables the proxy.
func UpdateProxy(raw string) (ProxyUpdate, error) {
	dir, err := UserDir()
	if err != nil {
		return ProxyUpdate{}, err
	}
	return UpdateProxyFrom(dir, raw)
}

// UpdateProxyFrom is UpdateProxy rooted at dir. It is exported for command
// tests and intentionally does not load the normal mutating config path: a
// malformed config must be reported, not backed up or reset by a proxy edit.
func UpdateProxyFrom(dir, raw string) (ProxyUpdate, error) {
	raw = strings.TrimSpace(raw)
	runtime, err := proxy.New(raw)
	if err != nil {
		return ProxyUpdate{}, err
	}
	result := ProxyUpdate{Enabled: runtime.Enabled(), Endpoint: runtime.Endpoint()}
	path := filepath.Join(dir, "config.yaml")

	data, mode, exists, err := readEditableConfig(path)
	if err != nil {
		return ProxyUpdate{}, err
	}
	if !exists && !runtime.Enabled() {
		// "proxy off" should not create a config directory merely to say that
		// there was nothing to turn off.
		return result, nil
	}

	var doc yaml.Node
	if exists {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			// YAML errors may quote the offending scalar. Do not risk echoing an
			// old credential-bearing proxy URL while handling a new proxy edit.
			return ProxyUpdate{}, errors.New("config is invalid YAML; fix it before changing the proxy")
		}
	} else {
		defaults := Default(dir)
		seed, err := yaml.Marshal(defaults)
		if err != nil {
			return ProxyUpdate{}, err
		}
		if err := yaml.Unmarshal(seed, &doc); err != nil {
			return ProxyUpdate{}, err
		}
	}

	changed, err := setProxyNode(&doc, raw)
	if err != nil {
		return ProxyUpdate{}, err
	}
	needsCredentialHardening := exists && runtime.HasCredentials() && mode&0o077 != 0
	if !changed && !needsCredentialHardening {
		return result, nil
	}
	if !exists {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ProxyUpdate{}, err
		}
		mode = 0o644
	}
	if runtime.HasCredentials() {
		mode = 0o600
	}
	if err := writeYAMLAtomic(path, &doc, mode); err != nil {
		return ProxyUpdate{}, err
	}
	result.Changed = changed || needsCredentialHardening
	return result, nil
}

// readEditableConfig refuses symlinks and non-regular files before a mutating
// operation. Following one here could replace a file outside ~/.tork when the
// atomic rename happens.
func readEditableConfig(path string) (data []byte, mode os.FileMode, exists bool, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, false, fmt.Errorf("refusing to edit symlinked config %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("refusing to edit non-regular config %s", path)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, false, err
	}
	return data, info.Mode().Perm(), true, nil
}

func setProxyNode(doc *yaml.Node, raw string) (bool, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return false, fmt.Errorf("config root must be a YAML mapping")
	}
	root := doc.Content[0]
	proxyIndex := mappingKey(root, "proxy")
	if raw == "" {
		if proxyIndex < 0 {
			return false, nil
		}
		proxyNode := root.Content[proxyIndex+1]
		if proxyNode.Kind != yaml.MappingNode {
			return false, fmt.Errorf("config proxy must be a YAML mapping")
		}
		socksIndex := mappingKey(proxyNode, "socks5")
		if socksIndex < 0 {
			return false, nil
		}
		proxyNode.Content = append(proxyNode.Content[:socksIndex], proxyNode.Content[socksIndex+2:]...)
		if len(proxyNode.Content) == 0 {
			root.Content = append(root.Content[:proxyIndex], root.Content[proxyIndex+2:]...)
		}
		return true, nil
	}

	var proxyNode *yaml.Node
	if proxyIndex < 0 {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "proxy"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"},
		)
		proxyNode = root.Content[len(root.Content)-1]
	} else {
		proxyNode = root.Content[proxyIndex+1]
		if proxyNode.Kind != yaml.MappingNode {
			return false, fmt.Errorf("config proxy must be a YAML mapping")
		}
	}
	socksIndex := mappingKey(proxyNode, "socks5")
	if socksIndex >= 0 {
		if proxyNode.Content[socksIndex+1].Value == raw {
			return false, nil
		}
		value := proxyNode.Content[socksIndex+1]
		value.Kind, value.Tag, value.Value, value.Style = yaml.ScalarNode, "!!str", raw, 0
		return true, nil
	}
	proxyNode.Content = append(proxyNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "socks5"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: raw},
	)
	return true, nil
}

func mappingKey(node *yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func writeYAMLAtomic(path string, doc *yaml.Node, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.yaml-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	enc := yaml.NewEncoder(tmp)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		_ = tmp.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// defaultDownloadDir resolves the OS "Downloads" folder, so torrents land
// where people already expect their downloads, tucked into a tork/ subfolder
// so they never get lost among browser files. Honors XDG_DOWNLOAD_DIR when set
// (the Linux convention, and a convenient override anywhere); otherwise
// ~/Downloads. Falls back to a relative path only if home can't be resolved.
func defaultDownloadDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DOWNLOAD_DIR")); xdg != "" {
		return filepath.Join(expandHome(xdg), "tork")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Downloads", "tork")
	}
	return "downloads"
}

// LoadFrom is Load with an explicit base dir (used by tests).
func LoadFrom(dir string) (*Config, error) {
	cfg := Default(dir)
	defaults := Default(dir)
	path := cfg.ConfigPath()

	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		out, merr := yaml.Marshal(cfg)
		if merr != nil {
			return nil, merr
		}
		if werr := os.WriteFile(path, out, 0o644); werr != nil {
			return nil, werr
		}
	case err != nil:
		return nil, err
	default:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			// Corrupt config must never stop the app from starting: preserve
			// the bad file, regenerate defaults, and carry on.
			_ = os.Rename(path, path+".bak")
			cfg = Default(dir)
			if out, merr := yaml.Marshal(cfg); merr == nil {
				_ = os.WriteFile(path, out, 0o644)
			}
			fmt.Fprintf(os.Stderr, "tork: %s was invalid YAML; backed up to %s.bak and reset to defaults\n", path, path)
		}
	}

	cfg.dir = dir
	mergeProviderDefaults(cfg, defaults)
	cfg.DownloadDir = expandHome(cfg.DownloadDir)
	if err := cfg.initProxyRuntime(); err != nil {
		return nil, err
	}
	if err := cfg.secureProxyCredentials(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadReadOnlyFrom is LoadReadOnly with an explicit base directory for tests.
// A missing config gets in-memory defaults; malformed or unreadable config is
// reported to the caller instead of being backed up and replaced.
func LoadReadOnlyFrom(dir string) (*Config, error) {
	cfg := Default(dir)
	defaults := Default(dir)
	path := cfg.ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("read %s: invalid YAML", path)
	}
	cfg.dir = dir
	mergeProviderDefaults(cfg, defaults)
	cfg.DownloadDir = expandHome(cfg.DownloadDir)
	if err := cfg.initProxyRuntime(); err != nil {
		// A diagnostic load must be able to report a bad proxy URL; failing
		// here would make doctor and `tork proxy status` abort with a bare
		// error instead of explaining it. The runtime stays the disabled
		// default, and ProxyError carries the (already credential-free)
		// reason. The mutating loader above still refuses to start.
		cfg.proxyErr = err
	}
	return cfg, nil
}

func (c *Config) initProxyRuntime() error {
	runtime, err := proxy.New(c.Proxy.SOCKS5)
	if err != nil {
		return err
	}
	c.proxyRuntime = runtime
	return nil
}

// secureProxyCredentials protects URL credentials in the normal, mutating
// loader. The read-only loader deliberately does not call this.
func (c *Config) secureProxyCredentials() error {
	if c.proxyRuntime == nil || !c.proxyRuntime.HasCredentials() {
		return nil
	}
	secure, err := c.ProxyCredentialConfigSecure()
	if err != nil {
		return fmt.Errorf("check proxy credential permissions: %w", err)
	}
	if secure {
		return nil
	}
	if err := os.Chmod(c.ConfigPath(), 0o600); err != nil {
		return fmt.Errorf("secure proxy credentials in %s: %w", c.ConfigPath(), err)
	}
	secure, err = c.ProxyCredentialConfigSecure()
	if err != nil {
		return fmt.Errorf("check proxy credential permissions: %w", err)
	}
	if !secure {
		return fmt.Errorf("refusing to use proxy credentials from %s: file must be owner-readable only", c.ConfigPath())
	}
	return nil
}

func mergeProviderDefaults(cfg, defaults *Config) {
	if cfg.Providers == nil {
		cfg.Providers = defaults.Providers
		return
	}
	for name, p := range defaults.Providers {
		if _, ok := cfg.Providers[name]; !ok {
			cfg.Providers[name] = p
		}
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
