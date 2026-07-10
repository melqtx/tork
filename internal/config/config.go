// Package config owns ~/.tork paths and the YAML config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

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
	MaxDownloads int `yaml:"max_downloads"`
	MinSeeders   int `yaml:"min_seeders"`
}

// HealthConfig tunes the opportunistic health check that runs on launch when
// the last recorded check is older than the interval.
type HealthConfig struct {
	Enabled       bool `yaml:"enabled"`
	IntervalHours int  `yaml:"interval_hours"`
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
	Providers             map[string]ProviderConfig `yaml:"providers"`

	dir string // ~/.tork, resolved at load time
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

func (c *Config) Dir() string                { return c.dir }
func (c *Config) StatePath() string          { return filepath.Join(c.dir, "state.json") }
func (c *Config) ConfigPath() string         { return filepath.Join(c.dir, "config.yaml") }
func (c *Config) HealthPath() string         { return filepath.Join(c.dir, "health.json") }
func (c *Config) PieceCompletionDir() string { return c.dir }

func Default(dir string) *Config {
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
		dir: dir,
	}
}

// Load reads ~/.tork/config.yaml, writing the default file on first run.
// Missing keys fall back to defaults. It also ensures the directories exist.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	return LoadFrom(filepath.Join(home, ".tork"))
}

// LoadReadOnly reads the user's configuration without creating, repairing, or
// otherwise touching any files or directories. Diagnostics use this path so a
// check can never change the machine it is inspecting.
func LoadReadOnly() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	return LoadReadOnlyFrom(filepath.Join(home, ".tork"))
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
			fmt.Fprintf(os.Stderr, "tork: %s was invalid (%v); backed up to %s.bak and reset to defaults\n", path, err, path)
		}
	}

	cfg.dir = dir
	mergeProviderDefaults(cfg, defaults)
	cfg.DownloadDir = expandHome(cfg.DownloadDir)
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
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg.dir = dir
	mergeProviderDefaults(cfg, defaults)
	cfg.DownloadDir = expandHome(cfg.DownloadDir)
	return cfg, nil
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
