package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/provider"
)

type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }

func (s stubProvider) Search(ctx context.Context, _ string, out chan<- provider.Result) error {
	select {
	case out <- provider.Result{Title: "hit", Provider: s.name}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func testEngine(t *testing.T) (*config.Config, *engine.Engine) {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return cfg, eng
}

// waitForSnapshots polls until the store holds n snapshots, or gives up.
func waitForSnapshots(t *testing.T, store *health.Store, n int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if len(store.Log().Snapshots) >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// The launch check runs in the background and records exactly one daily
// snapshot, without the caller ever waiting on the network.
func TestMaybeCheckHealthRunsWhenDue(t *testing.T) {
	cfg, eng := testEngine(t)
	cfg.Health.Enabled = true
	store := health.Open(cfg.HealthPath())
	providers := []provider.Provider{stubProvider{name: "stub"}}

	start := time.Now()
	maybeCheckHealth(context.Background(), cfg, store, providers, eng, 0)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("maybeCheckHealth blocked for %s; it must not delay startup", elapsed)
	}

	if !waitForSnapshots(t, store, 1, 5*time.Second) {
		t.Fatal("no snapshot was recorded by the background check")
	}
	snap := store.Log().Snapshots[0]
	if snap.Kind != health.KindDaily {
		t.Errorf("snapshot kind = %q, want %q", snap.Kind, health.KindDaily)
	}
	if len(snap.Providers) != 1 || !snap.Providers[0].OK {
		t.Errorf("snapshot providers = %+v, want one healthy probe", snap.Providers)
	}
	if store.Due(cfg.Health.Interval()) {
		t.Error("a fresh daily snapshot must clear the due flag")
	}
}

// A second launch inside the interval must not probe again.
func TestMaybeCheckHealthSkipsWhenNotDue(t *testing.T) {
	cfg, eng := testEngine(t)
	store := health.Open(cfg.HealthPath())
	if err := store.Append(health.Snapshot{At: time.Now(), Kind: health.KindDaily}); err != nil {
		t.Fatal(err)
	}

	maybeCheckHealth(context.Background(), cfg, store, []provider.Provider{stubProvider{name: "stub"}}, eng, 0)
	time.Sleep(300 * time.Millisecond) // give a wrongly-spawned goroutine time to write

	if n := len(store.Log().Snapshots); n != 1 {
		t.Fatalf("store holds %d snapshots, want the 1 that was already there", n)
	}
}

// Disabling health checks in config must stop them entirely.
func TestMaybeCheckHealthRespectsDisabled(t *testing.T) {
	cfg, eng := testEngine(t)
	cfg.Health.Enabled = false
	store := health.Open(cfg.HealthPath())

	maybeCheckHealth(context.Background(), cfg, store, []provider.Provider{stubProvider{name: "stub"}}, eng, 0)
	time.Sleep(300 * time.Millisecond)

	if n := len(store.Log().Snapshots); n != 0 {
		t.Fatalf("store holds %d snapshots with checks disabled, want 0", n)
	}
}

func TestMaybeCheckHealthCancelsDuringWarmup(t *testing.T) {
	cfg, eng := testEngine(t)
	cfg.Health.Enabled = true
	store := health.Open(cfg.HealthPath())
	ctx, cancel := context.WithCancel(context.Background())
	maybeCheckHealth(ctx, cfg, store, []provider.Provider{stubProvider{name: "stub"}}, eng, time.Hour)
	cancel()
	time.Sleep(100 * time.Millisecond)
	if n := len(store.Log().Snapshots); n != 0 {
		t.Fatalf("cancelled warmup recorded %d snapshots", n)
	}
}

// probeEngine must report a real bound port and leave nothing running.
func TestProbeEngine(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port, err := probeEngine(cfg)
	if err != nil {
		t.Fatalf("probeEngine: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("probeEngine returned port %d", port)
	}
	// The engine was closed, so a second probe can bind again.
	if _, err := probeEngine(cfg); err != nil {
		t.Fatalf("probeEngine did not release the client: %v", err)
	}
}

// slowProvider keeps a probe in flight until its context dies, then takes a
// visible moment to unwind - so a stop() that merely cancels, without waiting,
// is caught rather than getting away with it on timing.
type slowProvider struct{ unwound *atomic.Bool }

func (slowProvider) Name() string { return "slow" }

func (s slowProvider) Search(ctx context.Context, _ string, _ chan<- provider.Result) error {
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	s.unwound.Store(true)
	return ctx.Err()
}

// stop() must not return until the background check has fully unwound: the
// check samples live torrent stats, so a caller that closes the engine while it
// is still running races the client's teardown. run() leans on this to order
// its defers. Worth running under -race too.
func TestMaybeCheckHealthStopWaitsForTheProbe(t *testing.T) {
	cfg, eng := testEngine(t)
	cfg.Health.Enabled = true
	store := health.Open(cfg.HealthPath())
	var unwound atomic.Bool

	stop := maybeCheckHealth(context.Background(), cfg, store, []provider.Provider{slowProvider{&unwound}}, eng, 0)
	time.Sleep(50 * time.Millisecond) // let the probe get in flight
	stop()

	if !unwound.Load() {
		t.Fatal("stop() returned while the probe was still running; eng.Close() would race it")
	}
	// Sampling the engine is now the caller's alone - this is exactly the
	// window in which run() calls eng.Close().
	eng.Snapshots()

	if n := len(store.Log().Snapshots); n != 0 {
		t.Fatalf("a check cancelled mid-probe recorded %d snapshots, want 0", n)
	}
}

// A check disabled or not yet due still returns a usable stop function.
func TestMaybeCheckHealthStopIsAlwaysSafeToCall(t *testing.T) {
	cfg, eng := testEngine(t)
	cfg.Health.Enabled = false
	stop := maybeCheckHealth(context.Background(), cfg, health.Open(cfg.HealthPath()), nil, eng, 0)
	stop()
}
