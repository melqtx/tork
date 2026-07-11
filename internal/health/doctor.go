package health

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/proxy"
	"github.com/melqtx/tork/internal/state"
)

// Status is a check's verdict. Warn means "works, but look at this"; Fail
// means tork cannot do its job until it is fixed.
type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusFail
)

// Glyph is the leading mark on a doctor line.
func (s Status) Glyph() string {
	switch s {
	case StatusWarn:
		return "!"
	case StatusFail:
		return "✗"
	}
	return "✓"
}

// Check is one diagnostic line.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Report is a full doctor run.
type Report struct {
	Checks []Check
	// Probe is the provider round the run performed, so callers can persist it.
	Probe []ProviderProbe
}

// Failed reports whether any check hard-failed, which the CLI turns into a
// non-zero exit status.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// lowDiskBytes is the free-space floor below which we warn. Torrents are big;
// under 5 GiB a download is likely to stall halfway.
const lowDiskBytes = 5 << 30

// EngineProbe reports whether the torrent engine can start and which port it
// listens on. It is injected so RunDoctor stays testable without opening a
// real client (and without engine importing health, or vice versa).
type EngineProbe func(*config.Config) (port int, err error)

// EgressProbe verifies a configured proxy without making health depend on a
// particular public endpoint. Tests inject a local endpoint; the CLI uses
// ProbeProxyEgress.
type EgressProbe func(context.Context, *config.Config) (proxy.Egress, error)

// RunDoctor executes every diagnostic and returns them in display order. Its
// own checks never mutate anything - a broken state entry is reported, not
// repaired - though an engineProbe, being opt-in, does start a real client and
// so touches the piece-completion database.
func RunDoctor(ctx context.Context, cfg *config.Config, providers []provider.Provider, store *Store, engineProbe EngineProbe) Report {
	return runDoctor(ctx, cfg, providers, store, engineProbe, nil)
}

// RunDoctorWithEgressProbe is RunDoctor with an explicit, opt-in proxy egress
// check. The normal doctor contract remains free of this extra public request.
func RunDoctorWithEgressProbe(ctx context.Context, cfg *config.Config, providers []provider.Provider, store *Store, engineProbe EngineProbe, egressProbe EgressProbe) Report {
	return runDoctor(ctx, cfg, providers, store, engineProbe, egressProbe)
}

func runDoctor(ctx context.Context, cfg *config.Config, providers []provider.Provider, store *Store, engineProbe EngineProbe, egressProbe EgressProbe) Report {
	var rep Report
	add := func(name string, st Status, format string, args ...any) {
		rep.Checks = append(rep.Checks, Check{Name: name, Status: st, Detail: fmt.Sprintf(format, args...)})
	}

	if _, err := os.Stat(cfg.ConfigPath()); os.IsNotExist(err) {
		add("config", StatusWarn, "not created; using defaults in memory")
	} else if err != nil {
		add("config", StatusFail, "%s: %v", cfg.ConfigPath(), err)
	} else {
		add("config", StatusOK, "%s", cfg.ConfigPath())
	}
	rep.Checks = append(rep.Checks, checkProxy(cfg))
	if egressProbe != nil {
		rep.Checks = append(rep.Checks, checkProxyEgress(ctx, cfg, egressProbe))
	}
	rep.Checks = append(rep.Checks, checkDownloadDir(cfg))
	rep.Checks = append(rep.Checks, checkState(cfg))
	rep.Checks = append(rep.Checks, checkEngine(cfg, engineProbe))

	if cfg.ProxyError() != nil {
		add("providers", StatusWarn, "not checked; proxy config is invalid")
	} else if len(providers) == 0 {
		add("providers", StatusFail, "none enabled - searching will return nothing")
	} else {
		rep.Probe = ProbeProviders(ctx, providers, cfg.SearchTimeout())
		for _, p := range rep.Probe {
			rep.Checks = append(rep.Checks, providerCheck(p))
		}
		up := 0
		for _, p := range rep.Probe {
			if p.OK {
				up++
			}
		}
		if up == 0 {
			add("providers", StatusFail, "no enabled provider answered the health check")
		}
	}

	rep.Checks = append(rep.Checks, checkHistory(cfg, store))
	return rep
}

// ProbeProxyEgress is the production doctor probe. It uses exactly the same
// SOCKS-only HTTP transport as searches and direct downloads.
func ProbeProxyEgress(ctx context.Context, cfg *config.Config) (proxy.Egress, error) {
	return cfg.ProxyRuntime().VerifyEgress(ctx)
}

func checkProxy(cfg *config.Config) Check {
	c := Check{Name: "proxy"}
	if err := cfg.ProxyError(); err != nil {
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("invalid proxy config: %v (fix with 'tork proxy set' or 'tork proxy off')", err)
		return c
	}
	runtime := cfg.ProxyRuntime()
	if runtime == nil || !runtime.Enabled() {
		c.Status = StatusOK
		c.Detail = "not configured (run 'tork proxy tor' to route through Tor)"
		return c
	}
	secure, err := cfg.ProxyCredentialConfigSecure()
	if err != nil {
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("cannot inspect credential permissions: %v", err)
		return c
	}
	if !secure {
		c.Status = StatusFail
		c.Detail = "proxy credentials require config.yaml mode 0600"
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("SOCKS5 %s · strict TCP-only torrent mode", runtime.Endpoint())
	return c
}

func checkProxyEgress(ctx context.Context, cfg *config.Config, probe EgressProbe) Check {
	c := Check{Name: "proxy egress"}
	if cfg.ProxyError() != nil {
		c.Status = StatusWarn
		c.Detail = "not checked; proxy config is invalid"
		return c
	}
	runtime := cfg.ProxyRuntime()
	if runtime == nil || !runtime.Enabled() {
		c.Status = StatusWarn
		c.Detail = "not configured; skipped"
		return c
	}
	secure, err := cfg.ProxyCredentialConfigSecure()
	if err != nil || !secure {
		c.Status = StatusWarn
		c.Detail = "not checked; credential config is insecure"
		return c
	}
	egress, err := probe(ctx, cfg)
	if err != nil {
		if proxy.IsRouteFailure(err) {
			c.Status = StatusFail
			c.Detail = "proxy route unavailable"
			return c
		}
		c.Status = StatusWarn
		c.Detail = "verification service unavailable"
		return c
	}
	c.Status = StatusOK
	if egress.IsTor {
		c.Detail = fmt.Sprintf("egress IP %s · Tor verified · strict", egress.IP)
	} else {
		c.Detail = fmt.Sprintf("egress IP %s · SOCKS route verified (not a Tor exit) · strict", egress.IP)
	}
	return c
}

func providerCheck(p ProviderProbe) Check {
	c := Check{Name: p.Name}
	switch {
	case p.OK:
		c.Status = StatusOK
		c.Detail = fmt.Sprintf("%dms · %d results", p.LatencyMS, p.Results)
	case p.Blocked:
		// Blocked is the site's anti-bot layer, not a broken tork: other
		// providers still answer, so this is a warning.
		c.Status = StatusWarn
		c.Detail = "blocked by site protection"
	default:
		c.Status = StatusWarn
		c.Detail = p.Err
	}
	return c
}

func checkDownloadDir(cfg *config.Config) Check {
	c := Check{Name: "download dir"}
	dir := cfg.DownloadDir
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		c.Status = StatusFail
		c.Detail = dir + " does not exist"
		return c
	case err != nil:
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("%s: %v", dir, err)
		return c
	case !info.IsDir():
		c.Status = StatusFail
		c.Detail = dir + " is not a directory"
		return c
	}

	free, err := freeBytes(dir)
	if err != nil {
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("%s · free space unknown: %v", dir, err)
		return c
	}
	if free < lowDiskBytes {
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("%s · only %s free", dir, humanBytes(free))
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("%s · %s free", dir, humanBytes(free))
	return c
}

func checkState(cfg *config.Config) Check {
	c := Check{Name: "state"}
	path := cfg.StatePath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		c.Status = StatusOK
		c.Detail = "no downloads recorded yet"
		return c
	}
	// state.Load is deliberately forgiving - it renames a corrupt file and
	// returns an empty state. Doctor must not mutate, so read and parse here.
	st, err := parseState(path)
	if err != nil {
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("%s is corrupt: %v", path, err)
		return c
	}
	missing := 0
	for _, e := range st.Entries {
		if e.DataPath == "" {
			continue
		}
		if _, err := os.Stat(e.DataPath); err != nil {
			if _, perr := os.Stat(e.DataPath + ".part"); perr != nil {
				missing++
			}
		}
	}
	if missing > 0 {
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("%d entries · %d with missing data (relink or remove them)", len(st.Entries), missing)
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("%d entries", len(st.Entries))
	return c
}

func checkEngine(cfg *config.Config, probe EngineProbe) Check {
	c := Check{Name: "engine"}
	if probe == nil {
		c.Status = StatusOK
		c.Detail = "not checked"
		return c
	}
	port, err := probe(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "piece database is locked") {
			c.Status = StatusWarn
			c.Detail = "another tork instance appears to be running"
			return c
		}
		c.Status = StatusFail
		c.Detail = err.Error()
		return c
	}
	if port == 0 {
		if cfg.ProxyRuntime().Enabled() {
			c.Status = StatusOK
			c.Detail = "started · no inbound listener (strict proxy mode)"
			return c
		}
		// Without a proxy this is unexpected: the engine came up but bound
		// nothing, so inbound peers are silently impossible.
		c.Status = StatusWarn
		c.Detail = "started · no inbound listener"
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("listening on port %d", port)
	return c
}

func checkHistory(cfg *config.Config, store *Store) Check {
	c := Check{Name: "health history"}
	if err := store.LoadError(); err != nil {
		c.Status = StatusWarn
		c.Detail = "unreadable: " + err.Error()
		return c
	}
	if !cfg.Health.Enabled {
		c.Status = StatusOK
		c.Detail = "automatic checks disabled"
		return c
	}
	last, ok := store.LastDaily()
	if !ok {
		c.Status = StatusOK
		c.Detail = "no automatic check yet; the next launch will record one"
		return c
	}
	age := time.Since(last)
	// Two intervals of silence means the automatic check isn't landing - most
	// likely tork is never left open long enough for it to finish.
	if age > 2*cfg.Health.Interval() {
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("last check %s ago (expected every %s)", humanAge(age), humanAge(cfg.Health.Interval()))
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("last check %s ago", humanAge(age))
	return c
}

// parseState reads the resume list without the forgiving fallbacks of
// state.Load, so a corrupt file is reported instead of silently reset.
func parseState(path string) (*state.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// humanAge renders a duration the way a person would say it.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// FormatReport renders a report as plain aligned text for the terminal.
func FormatReport(rep Report) string {
	width := 0
	for _, c := range rep.Checks {
		width = max(width, len(c.Name))
	}
	var b strings.Builder
	for _, c := range rep.Checks {
		fmt.Fprintf(&b, "  %s %-*s  %s\n", c.Status.Glyph(), width, c.Name, c.Detail)
	}
	return b.String()
}
