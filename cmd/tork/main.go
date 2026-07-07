package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/autopilot"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
	"github.com/melqtx/tork/internal/tui"
)

// version is set at release time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// resolveVersion prefers the linker-injected version, then Go's embedded
// module version (so `go install …@vX.Y.Z` reports it correctly), then "dev".
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

func main() {
	// last-resort net: a panic that escaped every inner recover still exits
	// cleanly with a message instead of dumping a stack trace on the user.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "tork: fatal: %v\n", r)
			os.Exit(1)
		}
	}()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			printHelp()
			return
		case "-v", "--version", "version":
			fmt.Println("tork " + resolveVersion())
			return
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "autopilot" {
		if err := runAutopilot(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "tork:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tork:", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`tork - terminal torrent search and download

Usage:
  tork [-d DIR]
  tork autopilot [--dry-run] [--headless] [-n N] [-d DIR] "query"
  tork --version

Flags:
  -d, --download-dir DIR   where to save downloads (default: your OS
                           Downloads folder, ~/Downloads/tork)

The interactive UI stores config and state under ~/.tork and downloads into
your OS Downloads folder by default.
`)
}

// runAutopilot searches, picks the best downloads for an intent, and either
// queues them (handing off to the downloads TUI) or plans them (--dry-run).
func runAutopilot(args []string) error {
	fs := flag.NewFlagSet("autopilot", flag.ContinueOnError)
	maxN := fs.Int("n", 0, "max downloads (overrides config)")
	dryRun := fs.Bool("dry-run", false, "plan only; don't download")
	headless := fs.Bool("headless", false, "no TUI; print progress until complete")
	dlDir := fs.String("download-dir", "", "download directory (default: your OS Downloads/tork)")
	fs.StringVar(dlDir, "d", "", "shorthand for --download-dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if raw == "" {
		return errors.New(`usage: tork autopilot [-n N] [--dry-run] [--headless] "query"`)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	legacyDownloadDir := cfg.DownloadDir
	if *dlDir != "" {
		if err := cfg.OverrideDownloadDir(*dlDir); err != nil {
			return err
		}
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()
	st, err := state.Load(cfg.StatePath())
	if err != nil {
		return err
	}
	resumeAll(eng, st, cfg, legacyDownloadDir)

	providers := enabledProviders(cfg)
	agg := aggregator.New(providers, cfg.SearchTimeout(), 2)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dep := autopilot.Deps{Cfg: cfg, Agg: agg, Eng: eng, State: st, Providers: providers, Out: os.Stdout}
	queued, err := dep.Execute(ctx, raw, *dryRun, *maxN)
	if err != nil {
		return err
	}
	if *dryRun || len(queued) == 0 {
		return nil
	}

	if *headless {
		autopilot.RunHeadless(ctx, eng, os.Stdout)
		return st.Save(cfg.StatePath())
	}
	app := tui.New(cfg, eng, agg, st)
	app.ShowDownloads()
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return st.Save(cfg.StatePath())
}

func run() error {
	fs := flag.NewFlagSet("tork", flag.ContinueOnError)
	dlDir := fs.String("download-dir", "", "download directory (default: your OS Downloads/tork)")
	fs.StringVar(dlDir, "d", "", "shorthand for --download-dir")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	legacyDownloadDir := cfg.DownloadDir
	if *dlDir != "" {
		if err := cfg.OverrideDownloadDir(*dlDir); err != nil {
			return err
		}
	}

	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()

	st, err := state.Load(cfg.StatePath())
	if err != nil {
		return err
	}
	resumeAll(eng, st, cfg, legacyDownloadDir)

	agg := aggregator.New(enabledProviders(cfg), cfg.SearchTimeout(), 2)

	p := tea.NewProgram(tui.New(cfg, eng, agg, st), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return st.Save(cfg.StatePath())
}

// resumeAll re-adds persisted downloads; bolt piece completion (torrents) and
// .part files (direct downloads) make this a verification pass, not a
// re-download.
func resumeAll(eng *engine.Engine, st *state.State, cfg *config.Config, legacyDownloadDir string) {
	for i := range st.Entries {
		e := &st.Entries[i]
		normalizeEntryPaths(e, legacyDownloadDir)
		if e.Paused || e.NeedsRelink {
			continue
		}
		seed := e.SeedEnabled(cfg.SeedAfterComplete)
		if e.Done {
			if !entryDataPresent(*e) {
				continue
			}
			if !seed {
				continue
			}
		}
		opts := engine.AddOptions{DownloadDir: e.DownloadDir, Excluded: e.Excluded, Seed: &seed}
		if strings.HasPrefix(e.Magnet, "http://") || strings.HasPrefix(e.Magnet, "https://") {
			eng.AddDirectWithOptions(e.Magnet, e.Name, e.SHA256, opts) // best-effort; failures surface in UI
			continue
		}
		eng.AddWithOptions(e.Magnet, opts) // best-effort; failures surface in UI
	}
}

func normalizeEntryPaths(e *state.Entry, legacyDir string) {
	if e.DownloadDir == "" {
		inferLegacyEntryPath(e, legacyDir)
		return
	}
	if abs, err := filepath.Abs(e.DownloadDir); err == nil {
		e.DownloadDir = abs
	}
	if e.DataPath == "" && e.Name != "" && e.Name != "?" {
		e.DataPath = filepath.Join(e.DownloadDir, e.Name)
	}
}

func inferLegacyEntryPath(e *state.Entry, legacyDir string) {
	if e.Name == "" || e.Name == "?" || legacyDir == "" {
		e.NeedsRelink = true
		e.Paused = true
		return
	}
	if abs, err := filepath.Abs(legacyDir); err == nil {
		legacyDir = abs
	}
	candidate := filepath.Join(legacyDir, e.Name)
	if pathOrPartExists(candidate) {
		e.DownloadDir = legacyDir
		e.DataPath = candidate
		e.NeedsRelink = false
		return
	}
	e.DataPath = candidate
	e.NeedsRelink = true
	e.Paused = true
}

func entryDataPresent(e state.Entry) bool {
	if e.DataPath == "" {
		return false
	}
	return pathOrPartExists(e.DataPath)
}

func pathOrPartExists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Stat(path + ".part"); err == nil {
		return true
	}
	return false
}

func enabledProviders(cfg *config.Config) []provider.Provider {
	var out []provider.Provider

	for _, name := range []string{"knaben", "yts", "nyaa", "tpb_movies", "tpb_tv", "eztv", "x1337"} {
		p, ok := cfg.Providers[name]
		if !ok || !p.Enabled {
			continue
		}
		out = appendProvider(out, name, p)
	}

	var custom []string
	for name := range cfg.Providers {
		switch name {
		case "knaben", "yts", "nyaa", "tpb_movies", "tpb_tv", "eztv", "x1337":
			continue
		}
		custom = append(custom, name)
	}
	sort.Strings(custom)
	for _, name := range custom {
		p := cfg.Providers[name]
		if !p.Enabled {
			continue
		}
		out = appendProvider(out, name, p)
	}
	return out
}

func appendProvider(out []provider.Provider, name string, p config.ProviderConfig) []provider.Provider {
	switch providerType(name, p.Type) {
	case "knaben":
		return append(out, provider.NewKnaben(nil, p.Mirror))
	case "yts":
		return append(out, provider.NewYTS(nil, p.Mirror))
	case "nyaa":
		return append(out, provider.NewNyaa(nil, p.Mirror))
	case "eztv_html":
		return append(out, provider.NewEZTV(nil, p.Mirror))
	case "1337x_html":
		return append(out, provider.NewX1337(nil, p.BaseURLs()))
	case "tpb_movies":
		return append(out, provider.NewPirateBayMovies(nil, p.BaseURLs()...))
	case "tpb_tv":
		return append(out, provider.NewPirateBayTV(nil, p.BaseURLs()...))
	case "rss", "torznab":
		return append(out, provider.NewRSS(nil, name, p.SearchURL))
	}
	return out
}

func providerType(name, typ string) string {
	if typ != "" {
		return typ
	}
	switch name {
	case "knaben", "yts", "nyaa", "tpb_movies", "tpb_tv", "rss", "torznab":
		return name
	case "eztv":
		return "eztv_html"
	case "x1337":
		return "1337x_html"
	}
	return name
}
