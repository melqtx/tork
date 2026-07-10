package health

import "time"

// ProviderTrend is a provider's latest probe plus what history says about it.
type ProviderTrend struct {
	Name      string
	OK        bool
	Blocked   bool
	Err       string
	LatencyMS int64
	Results   int

	// Streak counts consecutive snapshots ending at the most recent one that
	// agree with the latest outcome: positive when up, negative when down.
	// "up 7" reads as a week of good days; "down 3" is a provider going bad.
	Streak int
	// History is latency per snapshot, oldest first, for a sparkline. A failed
	// probe contributes 0.
	History []int64
	Seen    bool // false when the provider has never been probed
	At      time.Time
}

// SwarmTrend is a library item's latest swarm plus its direction of travel.
type SwarmTrend struct {
	Hash    string
	Name    string
	Seeders int
	Peers   int
	// Delta is seeders now minus seeders in the previous snapshot that saw
	// this item; 0 when there is no earlier reading.
	Delta int
	Done  bool
	// Dying marks an unfinished download whose swarm has had no seeders in
	// every one of the last dyingWindow readings - the case worth warning
	// about, since it may never complete.
	Dying bool
	At    time.Time
}

// dyingWindow is how many consecutive seederless readings it takes before an
// unfinished download is called dying. Two guards against a single unlucky
// probe taken before peers connected.
const dyingWindow = 2

// dailies returns just the scheduled snapshots, oldest first. Doctor runs are
// excluded so an afternoon of debugging doesn't distort the trend.
func (l Log) dailies() []Snapshot {
	out := make([]Snapshot, 0, len(l.Snapshots))
	for _, s := range l.Snapshots {
		if s.Kind == KindDaily {
			out = append(out, s)
		}
	}
	return out
}

// Latest returns the most recent snapshot of any kind.
func (l Log) Latest() (Snapshot, bool) {
	if len(l.Snapshots) == 0 {
		return Snapshot{}, false
	}
	return l.Snapshots[len(l.Snapshots)-1], true
}

// latestWith returns the newest snapshot for which has() is true, along with
// its index. Not every snapshot carries every kind of reading - a `tork doctor`
// run probes providers but never samples swarms - so the two sections of the
// compass have to reach back independently rather than both trusting the last
// snapshot to be complete.
func (l Log) latestWith(has func(Snapshot) bool) (Snapshot, int, bool) {
	for i := len(l.Snapshots) - 1; i >= 0; i-- {
		if has(l.Snapshots[i]) {
			return l.Snapshots[i], i, true
		}
	}
	return Snapshot{}, -1, false
}

// LastCheck reports the time of the most recent snapshot of any kind, which is
// what the compass shows as "last check".
func (l Log) LastCheck() (time.Time, bool) {
	s, ok := l.Latest()
	return s.At, ok
}

// ProviderTrends summarizes each provider named in the most recent snapshot
// that probed any. Order follows that snapshot, which follows the configured
// provider order.
func (l Log) ProviderTrends() []ProviderTrend {
	latest, _, ok := l.latestWith(func(s Snapshot) bool { return len(s.Providers) > 0 })
	if !ok {
		return nil
	}
	hist := l.dailies()
	trends := make([]ProviderTrend, 0, len(latest.Providers))
	for _, p := range latest.Providers {
		t := ProviderTrend{
			Name: p.Name, OK: p.OK, Blocked: p.Blocked, Err: p.Err,
			LatencyMS: p.LatencyMS, Results: p.Results, Seen: true, At: latest.At,
		}
		for _, snap := range hist {
			if pp, found := findProvider(snap, p.Name); found {
				if pp.OK {
					t.History = append(t.History, pp.LatencyMS)
				} else {
					t.History = append(t.History, 0)
				}
			}
		}
		t.Streak = providerStreak(hist, p.Name, p.OK)
		trends = append(trends, t)
	}
	return trends
}

// providerStreak counts back from the newest scheduled snapshot while the
// outcome still matches want, then signs the count: up when want is true.
func providerStreak(hist []Snapshot, name string, want bool) int {
	n := 0
	for i := len(hist) - 1; i >= 0; i-- {
		p, ok := findProvider(hist[i], name)
		if !ok || p.OK != want {
			break
		}
		n++
	}
	if !want {
		return -n
	}
	return n
}

func findProvider(s Snapshot, name string) (ProviderProbe, bool) {
	for _, p := range s.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderProbe{}, false
}

// SwarmTrends summarizes each library item in the most recent snapshot that
// sampled any.
func (l Log) SwarmTrends() []SwarmTrend {
	latest, at, ok := l.latestWith(func(s Snapshot) bool { return len(s.Swarms) > 0 })
	if !ok {
		return nil
	}
	hist := l.swarmReadings(at)
	last := len(hist) - 1
	trends := make([]SwarmTrend, 0, len(latest.Swarms))
	for _, sw := range latest.Swarms {
		t := SwarmTrend{
			Hash: sw.Hash, Name: sw.Name,
			Seeders: sw.Seeders, Peers: sw.Peers, Done: sw.Done, At: latest.At,
		}
		if prev, found := findSwarmBefore(hist, last, sw.Hash); found {
			t.Delta = sw.Seeders - prev.Seeders
		}
		t.Dying = !sw.Done && seederlessRun(hist, sw.Hash) >= dyingWindow
		trends = append(trends, t)
	}
	return trends
}

// swarmReadings is the trend line behind the library section: every scheduled
// snapshot older than the one being displayed, then that snapshot itself, which
// therefore always sits last. Ad-hoc checks are otherwise excluded, because a
// burst of manual ones - two presses of `r`, seconds apart, before peers have
// connected - would manufacture a seederless run and label a healthy torrent
// dying, the exact misreading dyingWindow exists to prevent. Provider streaks
// filter through dailies() for the same reason.
func (l Log) swarmReadings(at int) []Snapshot {
	hist := make([]Snapshot, 0, at+1)
	for _, s := range l.Snapshots[:at] {
		if s.Kind == KindDaily {
			hist = append(hist, s)
		}
	}
	return append(hist, l.Snapshots[at])
}

// findSwarmBefore searches backwards from before index i for an earlier
// reading of the same torrent.
func findSwarmBefore(snaps []Snapshot, i int, hash string) (SwarmProbe, bool) {
	for j := i - 1; j >= 0; j-- {
		for _, sw := range snaps[j].Swarms {
			if sw.Hash == hash {
				return sw, true
			}
		}
	}
	return SwarmProbe{}, false
}

// seederlessRun counts consecutive most-recent readings of hash with no
// seeders, stopping at the first reading that had one.
func seederlessRun(snaps []Snapshot, hash string) int {
	n := 0
	for i := len(snaps) - 1; i >= 0; i-- {
		sw, ok := findSwarm(snaps[i], hash)
		if !ok {
			continue // this snapshot never saw the torrent; it breaks nothing
		}
		if sw.Seeders > 0 {
			break
		}
		n++
	}
	return n
}

func findSwarm(s Snapshot, hash string) (SwarmProbe, bool) {
	for _, sw := range s.Swarms {
		if sw.Hash == hash {
			return sw, true
		}
	}
	return SwarmProbe{}, false
}
