package health

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/melqtx/tork/internal/autopilot"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/provider"
)

// canaryQuery is a term every general torrent index has plenty of, so a zero
// result count is a real signal (the provider answered but found nothing)
// rather than an artifact of a query nobody indexes.
const canaryQuery = "1080p"

// ProbeProviders runs one canary search per provider, concurrently, and
// reports reachability and latency. It talks to providers directly rather than
// through the aggregator: the aggregator retries with backoff, which would
// fold a failed attempt's wait into the latency we are trying to measure.
//
// Latency is the wall time until Search returns, i.e. a full response, not
// time-to-first-result - a provider that streams one row fast and then stalls
// is not healthy.
func ProbeProviders(ctx context.Context, providers []provider.Provider, timeout time.Duration) []ProviderProbe {
	probes := make([]ProviderProbe, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			probes[i] = probeOne(ctx, p, timeout)
		}()
	}
	wg.Wait()
	return probes
}

// errPanicked stands in for a provider that blew up, without leaking a stack
// trace into the health log.
var errPanicked = errors.New("panicked")

func probeOne(ctx context.Context, p provider.Provider, timeout time.Duration) ProviderProbe {
	probe := ProviderProbe{Name: p.Name()}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Drain in the background: providers block on send, and we only need the
	// count. searchSafely closes out, so this goroutine always finishes.
	out := make(chan provider.Result)
	counted := make(chan int, 1)
	go func() {
		n := 0
		for range out {
			n++
		}
		counted <- n
	}()

	start := time.Now()
	err := searchSafely(ctx, p, out)
	probe.LatencyMS = time.Since(start).Milliseconds()
	probe.Results = <-counted

	if err != nil {
		probe.Blocked = errors.Is(err, provider.ErrBlocked)
		probe.Err = autopilot.ShortReason(err)
		return probe
	}
	probe.OK = true
	return probe
}

// searchSafely runs one canary search, always closing out and converting a
// panic into an error - so a hostile provider can neither take the probe down
// nor strand the goroutine counting its results.
func searchSafely(ctx context.Context, p provider.Provider, out chan<- provider.Result) (err error) {
	defer func() {
		close(out)
		if r := recover(); r != nil {
			err = errPanicked
		}
	}()
	return p.Search(ctx, canaryQuery, out)
}

// ProbeSwarms maps live engine snapshots into swarm records. Every persisted
// download is re-added to the client at startup, so this covers the library -
// except paused items, whose torrent is detached and whose peer gauges would
// read as a misleading zero.
func ProbeSwarms(snaps []engine.Snapshot) []SwarmProbe {
	var out []SwarmProbe
	for _, s := range snaps {
		if s.State == engine.StatePaused || s.State == engine.StateMissing {
			continue
		}
		out = append(out, SwarmProbe{
			Hash:    s.Hash.HexString(),
			Name:    s.Name,
			Seeders: s.Seeders,
			Active:  s.PeersActive,
			Peers:   s.PeersTotal,
			Done:    s.State == engine.StateDone || s.State == engine.StateSeeding,
		})
	}
	return out
}

// Run performs both probes and appends the result to the store.
//
// A cancelled context is never recorded. Every provider probe fails the instant
// ctx dies, so persisting that round would write a fleet of phantom failures -
// poisoning the streaks the compass reads, and (for a daily snapshot) resetting
// the due clock so the next real check is a day away.
func Run(ctx context.Context, kind string, providers []provider.Provider, snaps []engine.Snapshot, timeout time.Duration, store *Store) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		At:        time.Now().UTC(),
		Kind:      kind,
		Providers: ProbeProviders(ctx, providers, timeout),
		Swarms:    ProbeSwarms(snaps),
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return snap, store.Append(snap)
}
