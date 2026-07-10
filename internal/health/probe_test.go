package health

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/provider"
)

// fakeProvider answers a canary search however the test wants.
type fakeProvider struct {
	name    string
	results int
	err     error
	delay   time.Duration
	panics  bool
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Search(ctx context.Context, _ string, out chan<- provider.Result) error {
	if f.panics {
		panic("provider exploded")
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for i := 0; i < f.results; i++ {
		select {
		case out <- provider.Result{Title: "r", Provider: f.name}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func TestProbeProviders(t *testing.T) {
	providers := []provider.Provider{
		fakeProvider{name: "good", results: 3},
		fakeProvider{name: "empty"},
		fakeProvider{name: "blocked", err: provider.ErrBlocked},
		fakeProvider{name: "broken", err: errors.New("dial tcp: connection refused")},
	}
	probes := ProbeProviders(context.Background(), providers, time.Second)
	if len(probes) != 4 {
		t.Fatalf("got %d probes, want 4", len(probes))
	}
	// Order must mirror the provider slice so the compass lists them predictably.
	byName := map[string]ProviderProbe{}
	for i, p := range probes {
		if p.Name != providers[i].Name() {
			t.Fatalf("probe %d is %q, want %q (order not preserved)", i, p.Name, providers[i].Name())
		}
		byName[p.Name] = p
	}

	if g := byName["good"]; !g.OK || g.Results != 3 {
		t.Errorf("good provider = %+v, want OK with 3 results", g)
	}
	// Zero results is a healthy answer, not a failure.
	if e := byName["empty"]; !e.OK || e.Results != 0 {
		t.Errorf("empty provider = %+v, want OK with 0 results", e)
	}
	if b := byName["blocked"]; b.OK || !b.Blocked {
		t.Errorf("blocked provider = %+v, want not OK and Blocked", b)
	}
	if b := byName["broken"]; b.OK || b.Blocked || b.Err != "unreachable" {
		t.Errorf("broken provider = %+v, want not OK with a calm reason", b)
	}
}

// A provider that panics is unhealthy, never fatal.
func TestProbeProviderPanicIsContained(t *testing.T) {
	probes := ProbeProviders(context.Background(), []provider.Provider{fakeProvider{name: "boom", panics: true}}, time.Second)
	if len(probes) != 1 || probes[0].OK || probes[0].Err != "panicked" {
		t.Fatalf("panicking provider = %+v, want a contained failure", probes)
	}
}

// A provider slower than the timeout is reported as timed out, not hung.
func TestProbeProviderTimeout(t *testing.T) {
	start := time.Now()
	probes := ProbeProviders(context.Background(), []provider.Provider{
		fakeProvider{name: "slow", delay: 2 * time.Second},
	}, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %s; the timeout was not enforced", elapsed)
	}
	if probes[0].OK {
		t.Fatalf("slow provider reported OK: %+v", probes[0])
	}
}

func TestProbeSwarms(t *testing.T) {
	var h1, h2, h3 metainfo.Hash
	h1[0], h2[0], h3[0] = 1, 2, 3
	snaps := []engine.Snapshot{
		{Hash: h1, Name: "downloading", Seeders: 4, PeersActive: 6, PeersTotal: 10, State: engine.StateDownloading},
		{Hash: h2, Name: "seeding", Seeders: 0, State: engine.StateSeeding},
		// Paused torrents are detached from the client, so their peer gauges
		// would read as a misleading zero. They must be skipped.
		{Hash: h3, Name: "paused", State: engine.StatePaused},
	}
	got := ProbeSwarms(snaps)
	if len(got) != 2 {
		t.Fatalf("got %d swarm probes, want 2 (paused skipped)", len(got))
	}
	if got[0].Hash != h1.HexString() || got[0].Seeders != 4 || got[0].Peers != 10 || got[0].Done {
		t.Errorf("downloading swarm = %+v", got[0])
	}
	if !got[1].Done {
		t.Errorf("a seeding torrent must be marked done: %+v", got[1])
	}
}

func TestRunAppendsSnapshot(t *testing.T) {
	store := Open(t.TempDir() + "/health.json")
	snap, err := Run(context.Background(), KindDaily,
		[]provider.Provider{fakeProvider{name: "good", results: 1}},
		[]engine.Snapshot{{Name: "x", State: engine.StateDownloading}},
		time.Second, store)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if snap.Kind != KindDaily || len(snap.Providers) != 1 || len(snap.Swarms) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if n := len(store.Log().Snapshots); n != 1 {
		t.Fatalf("store holds %d snapshots, want 1", n)
	}
}

// A cancelled check must record nothing. Every probe fails the instant ctx
// dies, so persisting the round would write phantom provider failures - and a
// daily snapshot of them would also push the next real check a full day out.
func TestRunDoesNotRecordCancelledChecks(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "health.json"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := fakeProvider{name: "stub", results: 3}
	if _, err := Run(ctx, KindDaily, []provider.Provider{fake}, nil, time.Second, store); err == nil {
		t.Fatal("Run on a cancelled context must report an error")
	}
	if n := len(store.Log().Snapshots); n != 0 {
		t.Fatalf("cancelled check recorded %d snapshots, want 0", n)
	}
	if !store.Due(24 * time.Hour) {
		t.Error("a cancelled check must leave the daily check still due")
	}
}
