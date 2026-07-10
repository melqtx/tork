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
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/autopilot"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/health"
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
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		failed, err := runDoctor(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "tork:", err)
			os.Exit(1)
		}
		if failed {
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
  tork [--evil] [-d DIR]
  tork autopilot [--dry-run] [--headless] [--evil] [-n N] [-d DIR] "query"
  tork doctor [--record] [--engine] [-d DIR]
  tork --version

Commands:
  autopilot   search, pick the best sources, and queue them
  doctor      read-only config, disk, state, and provider diagnostic

Flags:
  -d, --download-dir DIR   where to save downloads (default: your OS
                           Downloads folder, ~/Downloads/tork)
      --evil               evil mode: downloads never seed after they finish
                           (applies to new downloads this session only)

The interactive UI stores config and state under ~/.tork and downloads into
your OS Downloads folder by default. Press H inside it for the health screen.
`)
}

// runDoctor prints a read-only diagnostic of the local setup and the provider
// fleet. It reports whether any check hard-failed so main can exit non-zero,
// which makes `tork doctor` usable as a cron canary.
func runDoctor(args []string) (failed bool, err error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dlDir := fs.String("download-dir", "", "download directory to check (default: the configured one)")
	fs.StringVar(dlDir, "d", "", "shorthand for --download-dir")
	record := fs.Bool("record", false, "record this provider check in health history")
	deepEngine := fs.Bool("engine", false, "start the torrent engine to verify its listener")
	if err := fs.Parse(args); err != nil {
		return false, err
	}

	cfg, err := config.LoadReadOnly()
	if err != nil {
		return false, err
	}
	if *dlDir != "" {
		// Deliberately not OverrideDownloadDir: a diagnostic reports on the
		// directory you named, it does not create it.
		cfg.SetDownloadDir(*dlDir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store := health.OpenReadOnly(cfg.HealthPath())
	fmt.Printf("tork %s · %s\n\n", resolveVersion(), cfg.Dir())
	var engineProbe health.EngineProbe
	if *deepEngine {
		engineProbe = probeEngine
	}
	rep := health.RunDoctor(ctx, cfg, enabledProviders(cfg), store, engineProbe)
	fmt.Print(health.FormatReport(rep))

	if *record && len(rep.Probe) > 0 {
		if err := store.Append(health.Snapshot{At: time.Now().UTC(), Kind: health.KindDoctor, Providers: rep.Probe}); err != nil {
			return rep.Failed(), fmt.Errorf("record health snapshot: %w", err)
		}
	}
	return rep.Failed(), nil
}

// probeEngine starts the torrent engine just long enough to prove it can bind
// its port, then closes it.
func probeEngine(cfg *config.Config) (int, error) {
	eng, err := engine.New(cfg)
	if err != nil {
		return 0, err
	}
	defer eng.Close()
	return eng.ListenPort(), nil
}

// healthWarmup is how long the background check waits before sampling swarms,
// giving resumed torrents time to find peers.
const healthWarmup = 45 * time.Second

// maybeCheckHealth runs the scheduled health check in the background when it is
// due, so a long-lived TUI session records one datapoint a day without ever
// blocking startup. The warmup delay gives resumed torrents time to connect to
// peers, otherwise every swarm would read as zero seeders and look dead.
//
// It returns a stop function that cancels the check and waits for it to unwind.
// Callers must run it before closing the engine: the check reads live torrent
// stats, and sampling a client that is tearing itself down is a data race.
func maybeCheckHealth(parent context.Context, cfg *config.Config, store *health.Store, providers []provider.Provider, eng *engine.Engine, warmup time.Duration) (stop func()) {
	if !cfg.Health.Enabled || !store.Due(cfg.Health.Interval()) {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A probe must never take the app down, but it must not vanish either.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "tork: background health check panicked: %v\n", r)
			}
		}()
		if warmup > 0 {
			select {
			case <-time.After(warmup):
			case <-ctx.Done():
				return
			}
		}
		runCtx, cancelRun := context.WithTimeout(ctx, 2*time.Minute)
		defer cancelRun()
		if _, err := health.Run(runCtx, health.KindDaily, providers, eng.Snapshots(), cfg.SearchTimeout(), store); err != nil && runCtx.Err() == nil {
			fmt.Fprintf(os.Stderr, "tork: background health check: %v\n", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
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
	evil := fs.Bool("evil", false, "evil mode: never seed after downloads complete")
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

	// See run(): evil mode only changes the default for downloads queued this
	// session, leaving the client-wide seed default and resumed torrents alone.
	if *evil {
		cfg.SeedAfterComplete = false
	}

	providers := enabledProviders(cfg)
	agg := aggregator.New(providers, cfg.SearchTimeout(), 2).
		WithFilter(provider.ContentFilter{HideNSFW: cfg.HideNSFW})

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

	store := health.Open(cfg.HealthPath())
	if *headless {
		autopilot.RunHeadless(ctx, eng, os.Stdout)
		// Headless runs exit as soon as downloads finish, so a warmup delay
		// would usually outlive the process. Sample immediately: the swarms
		// were just active, and the providers were just exercised. A ctx that
		// died to Ctrl-C skips the check rather than recording a fleet of
		// phantom failures; health.Run enforces that.
		if cfg.Health.Enabled && store.Due(cfg.Health.Interval()) {
			_, _ = health.Run(ctx, health.KindDaily, providers, eng.Snapshots(), cfg.SearchTimeout(), store)
		}
		return st.Save(cfg.StatePath())
	}
	// Registered after `defer eng.Close()`, so it unwinds first and the check
	// is always finished before the torrent client goes away.
	stopHealth := maybeCheckHealth(context.Background(), cfg, store, providers, eng, healthWarmup)
	defer stopHealth()

	app := tui.New(cfg, eng, agg, st, store)
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
	evil := fs.Bool("evil", false, "evil mode: never seed after downloads complete")
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

	// Evil mode applies only to new downloads made this session. Set it after
	// engine.New (which has already captured the client-wide seed default) and
	// after resumeAll (which restores each persisted torrent's own seed choice),
	// so existing seeds keep seeding and only fresh downloads default to no-seed.
	if *evil {
		cfg.SeedAfterComplete = false
	}

	providers := enabledProviders(cfg)
	agg := aggregator.New(providers, cfg.SearchTimeout(), 2).
		WithFilter(provider.ContentFilter{HideNSFW: cfg.HideNSFW})

	store := health.Open(cfg.HealthPath())
	// Registered after `defer eng.Close()`, so it unwinds first and the check
	// is always finished before the torrent client goes away.
	stopHealth := maybeCheckHealth(context.Background(), cfg, store, providers, eng, healthWarmup)
	defer stopHealth()

	p := tea.NewProgram(tui.New(cfg, eng, agg, st, store), tea.WithAltScreen())
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
