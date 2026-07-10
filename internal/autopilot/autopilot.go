package autopilot

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

// ShortReason turns a raw provider/network error into a calm, human phrase
// instead of dumping a full Go error (URLs, "dial tcp", etc.) on the user.
func ShortReason(err error) string {
	if err == nil {
		return "failed"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "name resolution"):
		return "unreachable (dns)"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return "timed out"
	case strings.Contains(s, "blocked"):
		return "blocked"
	case strings.Contains(s, "connection refused"), strings.Contains(s, "no route"), strings.Contains(s, "dial "):
		return "unreachable"
	case strings.Contains(s, "unexpected status"):
		return "bad response"
	case strings.Contains(s, "eof"), strings.Contains(s, "connection reset"):
		// The server hung up mid-response - common when a site throttles us.
		return "no response"
	}
	if r := []rune(err.Error()); len(r) > 44 {
		return string(r[:44]) + "…"
	}
	return err.Error()
}

// Deps carries everything autopilot needs to search and queue downloads.
type Deps struct {
	Cfg       *config.Config
	Agg       *aggregator.Aggregator
	Eng       *engine.Engine
	State     *state.State
	Providers []provider.Provider // for lazy magnet resolution
	Out       io.Writer
}

// Execute parses a request, searches all providers, ranks and selects the
// best downloads, prints a report, and (unless dryRun) resolves magnets and
// queues them. maxOverride > 0 replaces the configured download cap.
// It returns the picks that were queued.
func (d Deps) Execute(ctx context.Context, raw string, dryRun bool, maxOverride int) ([]Pick, error) {
	in := ParseIntent(raw)
	in.MinSeeders = d.Cfg.Autopilot.MinSeeders
	in.Max = d.Cfg.Autopilot.MaxDownloads
	if maxOverride > 0 {
		in.Max = maxOverride
	}

	fmt.Fprintf(d.Out, "autopilot: %q\n", raw)
	fmt.Fprintf(d.Out, "  parsed → query=%q  resolution=%s  season=%s  max=%d  min-seeders=%d\n\n",
		in.Query, resStr(in), seasonStr(in), in.Max, in.MinSeeders)

	results := d.gather(ctx, in.Query)
	fmt.Fprintf(d.Out, "\n%d results gathered\n", len(results))

	known := knownHashes(d.State)
	picks := Select(results, in, d.Cfg.Ranking, known)
	if len(picks) == 0 {
		fmt.Fprintln(d.Out, "\nno suitable downloads found (try lowering min_seeders or relaxing the query)")
		return nil, nil
	}
	printPicks(d.Out, picks)

	if dryRun {
		fmt.Fprintln(d.Out, "\ndry run - nothing queued")
		return picks, nil
	}

	fmt.Fprintln(d.Out, "\nqueuing…")
	queued := picks[:0]
	for _, p := range picks {
		magnet, err := d.resolve(ctx, p.Result)
		if err != nil {
			fmt.Fprintf(d.Out, "  ✗ %s: %s\n", trunc(p.Result.Title, 50), ShortReason(err))
			continue
		}
		seed := d.Cfg.SeedAfterComplete
		h, err := d.Eng.AddWithOptions(magnet, engine.AddOptions{DownloadDir: d.Cfg.DownloadDir, Seed: &seed})
		if err != nil {
			fmt.Fprintf(d.Out, "  ✗ %s: %s\n", trunc(p.Result.Title, 50), ShortReason(err))
			continue
		}
		entry := state.Entry{
			Magnet:      magnet,
			Name:        p.Result.Title,
			AddedAt:     time.Now().UTC(),
			DownloadDir: d.Cfg.DownloadDir,
			Seed:        state.Bool(seed),
		}
		if snap, ok := d.Eng.Snapshot(h); ok {
			if snap.Name != "" && snap.Name != "?" {
				entry.Name = snap.Name
			}
			entry.DownloadDir = snap.DownloadDir
			entry.DataPath = snap.DataPath
			entry.Seed = state.Bool(snap.Seed)
			entry.BytesCompleted = snap.BytesCompleted
			entry.Length = snap.Length
		}
		d.State.Upsert(entry)
		fmt.Fprintf(d.Out, "  ✓ %s\n", trunc(p.Result.Title, 60))
		queued = append(queued, p)
	}
	return queued, d.State.Save(d.Cfg.StatePath())
}

// gather runs the aggregator to completion, printing per-provider status.
func (d Deps) gather(ctx context.Context, query string) []provider.Result {
	results, status := d.Agg.Search(ctx, query)
	var out []provider.Result
	for results != nil || status != nil {
		select {
		case r, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			out = append(out, r)
		case ev, ok := <-status:
			if !ok {
				status = nil
				continue
			}
			switch ev.State {
			case aggregator.StateDone:
				if ev.Count > 0 {
					fmt.Fprintf(d.Out, "  %-10s ✓ %d\n", ev.Provider, ev.Count)
				} else {
					fmt.Fprintf(d.Out, "  %-10s · no matches\n", ev.Provider)
				}
			case aggregator.StateFailed:
				fmt.Fprintf(d.Out, "  %-10s ✗ %s\n", ev.Provider, ShortReason(ev.Err))
			}
		}
	}
	return out
}

// resolve returns a usable magnet, resolving a detail-page result on demand.
func (d Deps) resolve(ctx context.Context, r provider.Result) (string, error) {
	if r.Magnet != "" {
		return r.Magnet, nil
	}
	for _, p := range d.Providers {
		if p.Name() != r.Provider {
			continue
		}
		if mr, ok := p.(provider.MagnetResolver); ok {
			return mr.ResolveMagnet(ctx, r)
		}
	}
	return "", fmt.Errorf("%s: no magnet and no resolver", r.Provider)
}

// RunHeadless polls download progress and prints a line per torrent every 2s
// until all complete or ctx is cancelled.
func RunHeadless(ctx context.Context, eng *engine.Engine, out io.Writer) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		snaps := eng.Snapshots()
		done := 0
		for _, s := range snaps {
			pct := s.Progress() * 100
			fmt.Fprintf(out, "  %-40s %5.1f%%  %s\n", trunc(s.Name, 40), pct, s.State)
			if s.State == engine.StateSeeding || s.State == engine.StateDone {
				done++
			}
		}
		fmt.Fprintln(out, "  ---")
		if len(snaps) > 0 && done == len(snaps) {
			fmt.Fprintln(out, "all downloads complete")
			return
		}
	}
}

func knownHashes(st *state.State) map[metainfo.Hash]bool {
	known := map[metainfo.Hash]bool{}
	if st == nil {
		return known
	}
	for _, e := range st.Entries {
		if m, err := metainfo.ParseMagnetUri(e.Magnet); err == nil {
			known[m.InfoHash] = true
		}
	}
	return known
}

func printPicks(out io.Writer, picks []Pick) {
	fmt.Fprintf(out, "\nselected %d download(s):\n", len(picks))
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  TITLE\tPROVIDER\tSIZE\tSEED\tSCORE\tWHY")
	for _, p := range picks {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%.0f\t%s\n",
			trunc(p.Result.Title, 48), p.Result.Provider, p.Result.Size,
			p.Result.Seeders, p.Score, p.Reason)
	}
	tw.Flush()
}

func resStr(in Intent) string {
	if s := in.WantRes.String(); s != "" {
		return s
	}
	return "any"
}

func seasonStr(in Intent) string {
	switch {
	case in.AllSeasons:
		return "all"
	case in.Season > 0:
		return fmt.Sprintf("%d", in.Season)
	}
	return "-"
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
