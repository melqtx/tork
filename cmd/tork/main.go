package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/autopilot"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/intake"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/proxy"
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
	if len(os.Args) > 1 && os.Args[1] == "proxy" {
		if err := runProxy(os.Args[2:]); err != nil {
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
  tork [--evil] [-d DIR] [MAGNET | INFOHASH | TORRENT_URL | TORRENT_FILE]
  tork [--evil] [-d DIR] --torrent-url URL
  tork autopilot [--dry-run] [--headless] [--yes] [--evil] [-n N] [-d DIR] "query"
  tork doctor [--record] [--engine] [--proxy-check] [-d DIR]
  tork proxy tor|set [SOCKS5_URL]|off|status
  tork --version

Commands:
  autopilot   search, explain the best choices, then queue them
  doctor      read-only config, disk, state, and provider diagnostic
  proxy       configure or inspect strict SOCKS5 routing

Flags:
  -d, --download-dir DIR   where to save downloads (default: your OS
                           Downloads folder, ~/Downloads/tork)
      --torrent-url URL    explicit torrent endpoint when its path does not
                           end in .torrent
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
	proxyCheck := fs.Bool("proxy-check", false, "verify proxy egress through the Tor Project check service")
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
	var rep health.Report
	if *proxyCheck {
		rep = health.RunDoctorWithEgressProbe(ctx, cfg, enabledProviders(cfg), store, engineProbe, health.ProbeProxyEgress)
	} else {
		rep = health.RunDoctor(ctx, cfg, enabledProviders(cfg), store, engineProbe)
	}
	fmt.Print(health.FormatReport(rep))

	if *record && len(rep.Probe) > 0 {
		if err := store.Append(health.Snapshot{At: time.Now().UTC(), Kind: health.KindDoctor, Providers: rep.Probe}); err != nil {
			return rep.Failed(), fmt.Errorf("record health snapshot: %w", err)
		}
	}
	return rep.Failed(), nil
}

func runProxy(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tork proxy tor|set [SOCKS5_URL]|off|status")
	}
	switch args[0] {
	case "tor":
		if len(args) != 1 {
			return errors.New("usage: tork proxy tor")
		}
		return saveProxy("socks5://127.0.0.1:9050")
	case "set":
		raw, err := proxyURLFromArgs(args[1:])
		if err != nil {
			return err
		}
		return saveProxy(raw)
	case "off":
		if len(args) != 1 {
			return errors.New("usage: tork proxy off")
		}
		update, err := config.UpdateProxy("")
		if err != nil {
			return err
		}
		if update.Changed {
			fmt.Println("proxy disabled; tork will use its normal direct mode next launch")
		} else {
			fmt.Println("proxy is already disabled")
		}
		return nil
	case "status":
		if len(args) != 1 {
			return errors.New("usage: tork proxy status")
		}
		return printProxyStatus()
	default:
		return errors.New("usage: tork proxy tor|set [SOCKS5_URL]|off|status")
	}
}

func proxyURLFromArgs(args []string) (string, error) {
	switch len(args) {
	case 0:
		if !term.IsTerminal(os.Stdin.Fd()) {
			return "", errors.New("run 'tork proxy set SOCKS5_URL' or enter a credentialed URL from an interactive terminal")
		}
		fmt.Fprint(os.Stderr, "SOCKS5 URL (hidden input): ")
		input, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read proxy URL: %w", err)
		}
		raw := strings.TrimSpace(string(input))
		if raw == "" {
			return "", errors.New("SOCKS5 URL is required")
		}
		return raw, nil
	case 1:
		raw := args[0]
		runtime, err := proxy.New(raw)
		if err != nil {
			return "", err
		}
		if runtime.HasCredentials() {
			return "", errors.New("for a credentialed proxy, run 'tork proxy set' and enter the URL privately")
		}
		return raw, nil
	default:
		return "", errors.New("usage: tork proxy set [SOCKS5_URL]")
	}
}

func saveProxy(raw string) error {
	update, err := config.UpdateProxy(raw)
	if err != nil {
		return err
	}
	if !update.Changed {
		fmt.Printf("proxy is already SOCKS5 %s · strict TCP-only torrent mode\n", update.Endpoint)
		return nil
	}
	fmt.Printf("proxy configured: SOCKS5 %s · strict TCP-only torrent mode\n", update.Endpoint)
	fmt.Println("verify it with: tork doctor --proxy-check")
	return nil
}

func printProxyStatus() error {
	cfg, err := config.LoadReadOnly()
	if err != nil {
		return err
	}
	if err := cfg.ProxyError(); err != nil {
		return fmt.Errorf("proxy config is invalid: %v (fix with 'tork proxy set SOCKS5_URL' or 'tork proxy off')", err)
	}
	runtime := cfg.ProxyRuntime()
	if runtime == nil || !runtime.Enabled() {
		fmt.Println("proxy: not configured (run 'tork proxy tor' to route through Tor)")
		return nil
	}
	secure, err := cfg.ProxyCredentialConfigSecure()
	if err != nil {
		return err
	}
	fmt.Printf("proxy: SOCKS5 %s · strict TCP-only torrent mode\n", runtime.Endpoint())
	fmt.Println("verify it with: tork doctor --proxy-check")
	if !secure {
		return errors.New("proxy credentials require config.yaml mode 0600")
	}
	return nil
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
	yes := fs.Bool("yes", false, "queue the plan without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	minSeeders := fs.Int("min-seeders", -1, "minimum seeders (overrides config)")
	maxSize := fs.String("max-size", "", "largest allowed download, for example 8GB")
	categories := fs.String("category", "", "comma-separated allowed provider categories")
	dlDir := fs.String("download-dir", "", "download directory (default: your OS Downloads/tork)")
	fs.StringVar(dlDir, "d", "", "shorthand for --download-dir")
	evil := fs.Bool("evil", false, "evil mode: never seed after downloads complete")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if raw == "" {
		return errors.New(`usage: tork autopilot [-n N] [--max-size 8GB] [--dry-run] [--headless] [--yes] "query"`)
	}
	var maxSizeBytes int64
	if strings.TrimSpace(*maxSize) != "" {
		parsed, parseErr := autopilot.ParseSizeLimit(*maxSize)
		if parseErr != nil {
			return errors.New("--max-size must look like 750MB, 8GB, or 1.5TB")
		}
		maxSizeBytes = parsed
	}
	var allowedCategories []string
	for _, category := range strings.Split(*categories, ",") {
		if category = strings.TrimSpace(category); category != "" {
			allowedCategories = append(allowedCategories, category)
		}
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
	opts := autopilot.Options{
		DryRun: *dryRun, MaxDownloads: *maxN,
		MaxSizeBytes: maxSizeBytes, Categories: allowedCategories,
	}
	if *minSeeders >= 0 {
		opts.MinSeeders = *minSeeders
		opts.OverrideMinSeeders = true
	}
	// --headless controls output, not consent: it still confirms on a
	// terminal and still requires --yes everywhere else.
	if !*dryRun && !*yes {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return errors.New("autopilot needs confirmation; rerun with --yes for non-interactive use")
		}
		opts.Confirm = confirmAutopilot
	}
	plan, err := dep.Execute(ctx, raw, opts)
	if err != nil {
		return err
	}
	if *dryRun || plan.Queued == 0 {
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

func confirmAutopilot(plan autopilot.Plan) bool {
	fmt.Fprintf(os.Stdout, "\nqueue these %d download(s)? [y/N] ", len(plan.Picks))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func run() error {
	fs := flag.NewFlagSet("tork", flag.ContinueOnError)
	dlDir := fs.String("download-dir", "", "download directory (default: your OS Downloads/tork)")
	fs.StringVar(dlDir, "d", "", "shorthand for --download-dir")
	evil := fs.Bool("evil", false, "evil mode: never seed after downloads complete")
	torrentURL := fs.String("torrent-url", "", "explicit .torrent download URL (allows non-.torrent paths)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	openTarget, err := resolveOpenTarget(fs.Args(), *torrentURL)
	if err != nil {
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

	app := tui.New(cfg, eng, agg, st, store)
	if openTarget != nil {
		if err := app.OpenTarget(*openTarget); err != nil {
			return err
		}
	}
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return st.Save(cfg.StatePath())
}

func resolveOpenTarget(args []string, explicitURL string) (*intake.Target, error) {
	if len(args) > 1 || (explicitURL != "" && len(args) != 0) {
		return nil, errors.New("usage: tork [--evil] [-d DIR] [MAGNET | INFOHASH | TORRENT_URL | TORRENT_FILE]")
	}
	if explicitURL != "" {
		target, err := intake.ExplicitTorrentURL(explicitURL)
		if err != nil {
			return nil, err
		}
		return &target, nil
	}
	if len(args) == 0 {
		return nil, nil
	}
	target, ok, err := intake.DetectCLI(args[0])
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("expected a magnet link, infohash, torrent URL, or local torrent file")
	}
	return &target, nil
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
	client := cfg.ProxyHTTPClient()

	for _, name := range []string{"knaben", "yts", "nyaa", "tpb_movies", "tpb_tv", "eztv", "x1337"} {
		p, ok := cfg.Providers[name]
		if !ok || !p.Enabled {
			continue
		}
		out = appendProvider(out, client, name, p)
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
		out = appendProvider(out, client, name, p)
	}
	return out
}

func appendProvider(out []provider.Provider, client *http.Client, name string, p config.ProviderConfig) []provider.Provider {
	switch providerType(name, p.Type) {
	case "knaben":
		return append(out, provider.NewKnaben(client, p.Mirror))
	case "yts":
		return append(out, provider.NewYTS(client, p.Mirror))
	case "nyaa":
		return append(out, provider.NewNyaa(client, p.Mirror))
	case "eztv_html":
		return append(out, provider.NewEZTV(client, p.Mirror))
	case "1337x_html":
		return append(out, provider.NewX1337(client, p.BaseURLs()))
	case "tpb_movies":
		return append(out, provider.NewPirateBayMovies(client, p.BaseURLs()...))
	case "tpb_tv":
		return append(out, provider.NewPirateBayTV(client, p.BaseURLs()...))
	case "rss", "torznab":
		return append(out, provider.NewRSS(client, name, p.SearchURL))
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
