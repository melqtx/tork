package health

import (
	"testing"
	"time"
)

func daily(providers []ProviderProbe, swarms []SwarmProbe) Snapshot {
	return Snapshot{Kind: KindDaily, Providers: providers, Swarms: swarms}
}

func up(name string, ms int64) ProviderProbe {
	return ProviderProbe{Name: name, OK: true, LatencyMS: ms, Results: 20}
}

func down(name string) ProviderProbe {
	return ProviderProbe{Name: name, Err: "timed out"}
}

func TestProviderStreaks(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily([]ProviderProbe{up("knaben", 100), down("nyaa")}, nil),
		daily([]ProviderProbe{up("knaben", 110), down("nyaa")}, nil),
		daily([]ProviderProbe{up("knaben", 120), down("nyaa")}, nil),
	}}
	trends := log.ProviderTrends()
	if len(trends) != 2 {
		t.Fatalf("got %d trends, want 2", len(trends))
	}

	knaben, nyaa := trends[0], trends[1]
	if knaben.Streak != 3 {
		t.Errorf("knaben streak = %d, want 3 (three good checks)", knaben.Streak)
	}
	if nyaa.Streak != -3 {
		t.Errorf("nyaa streak = %d, want -3 (three bad checks)", nyaa.Streak)
	}
	if knaben.LatencyMS != 120 {
		t.Errorf("knaben latency = %d, want the newest (120)", knaben.LatencyMS)
	}
	// A failed probe contributes a 0 to the sparkline rather than a gap.
	if want := []int64{0, 0, 0}; len(nyaa.History) != 3 || nyaa.History[0] != want[0] {
		t.Errorf("nyaa history = %v, want three zeroes", nyaa.History)
	}
	if len(knaben.History) != 3 || knaben.History[2] != 120 {
		t.Errorf("knaben history = %v, want [100 110 120]", knaben.History)
	}
}

// A streak counts only the run that reaches the newest check: a provider that
// just recovered is "up 1", not "up 3".
func TestProviderStreakBreaksOnFlip(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily([]ProviderProbe{down("yts")}, nil),
		daily([]ProviderProbe{down("yts")}, nil),
		daily([]ProviderProbe{up("yts", 90)}, nil),
	}}
	if got := log.ProviderTrends()[0].Streak; got != 1 {
		t.Fatalf("streak = %d, want 1 (recovered on the last check)", got)
	}
}

// Doctor runs are on-demand and must not enter the trend history, or an
// afternoon of debugging would swamp the daily signal.
func TestTrendsIgnoreDoctorSnapshotsInStreaks(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily([]ProviderProbe{up("knaben", 100)}, nil),
		{Kind: KindDoctor, Providers: []ProviderProbe{down("knaben")}},
	}}
	tr := log.ProviderTrends()[0]
	// The latest reading (the doctor run) still drives the current status...
	if tr.OK {
		t.Error("latest status should come from the newest snapshot, even a doctor run")
	}
	// ...but the streak is computed over scheduled checks only, where the only
	// entry is a success, so it must not report a failing streak.
	if tr.Streak < 0 {
		t.Errorf("streak = %d, want a non-negative value from the daily history", tr.Streak)
	}
}

func TestSwarmDeltaAndDying(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily(nil, []SwarmProbe{{Hash: "a", Name: "alive", Seeders: 5}, {Hash: "b", Name: "doomed", Seeders: 0}}),
		daily(nil, []SwarmProbe{{Hash: "a", Name: "alive", Seeders: 9}, {Hash: "b", Name: "doomed", Seeders: 0}}),
	}}
	trends := log.SwarmTrends()
	if len(trends) != 2 {
		t.Fatalf("got %d swarm trends, want 2", len(trends))
	}

	alive, doomed := trends[0], trends[1]
	if alive.Delta != 4 {
		t.Errorf("alive delta = %d, want +4 (5 -> 9)", alive.Delta)
	}
	if alive.Dying {
		t.Error("a swarm with seeders must not be marked dying")
	}
	if doomed.Delta != 0 {
		t.Errorf("doomed delta = %d, want 0", doomed.Delta)
	}
	if !doomed.Dying {
		t.Error("an unfinished swarm seederless across two checks must be dying")
	}
}

// One seederless reading is not enough: a probe taken before peers connect
// would otherwise condemn a healthy download.
func TestDyingNeedsTwoReadings(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 4}}),
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 0}}),
	}}
	if log.SwarmTrends()[0].Dying {
		t.Fatal("a single seederless reading must not mark a swarm dying")
	}
}

// A completed download that seeds to nobody is idle, not dying - there is
// nothing left to fail to fetch.
func TestFinishedSwarmIsNeverDying(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 0, Done: true}}),
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 0, Done: true}}),
	}}
	if log.SwarmTrends()[0].Dying {
		t.Fatal("a finished download must never be marked dying")
	}
}

// The CLI doctor probes providers but never samples swarms. Its snapshot must
// not blank out the library section of the compass.
func TestSwarmTrendsReachPastProviderOnlySnapshots(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		{At: time.Now().Add(-24 * time.Hour), Kind: KindDaily, Swarms: []SwarmProbe{{Hash: "a", Name: "movie", Seeders: 7}}},
		{At: time.Now(), Kind: KindDoctor, Providers: []ProviderProbe{up("knaben", 100)}},
	}}
	trends := log.SwarmTrends()
	if len(trends) != 1 || trends[0].Name != "movie" {
		t.Fatalf("swarm trends = %+v, want the reading from before the doctor run", trends)
	}
	if age := time.Since(trends[0].At); age < 23*time.Hour {
		t.Fatalf("swarm timestamp = %s ago, want the older daily sample", age)
	}
}

// Symmetrically, a swarm-only snapshot must not hide the provider fleet.
func TestProviderTrendsReachPastSwarmOnlySnapshots(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily([]ProviderProbe{up("knaben", 100)}, nil),
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 1}}),
	}}
	trends := log.ProviderTrends()
	if len(trends) != 1 || trends[0].Name != "knaben" {
		t.Fatalf("provider trends = %+v, want knaben", trends)
	}
}

func TestTrendsOnEmptyLog(t *testing.T) {
	var log Log
	if got := log.ProviderTrends(); got != nil {
		t.Errorf("ProviderTrends on empty log = %v, want nil", got)
	}
	if got := log.SwarmTrends(); got != nil {
		t.Errorf("SwarmTrends on empty log = %v, want nil", got)
	}
	if _, ok := log.LastCheck(); ok {
		t.Error("LastCheck on empty log must report no check")
	}
}

func manual(swarms []SwarmProbe) Snapshot {
	return Snapshot{Kind: KindManual, Swarms: swarms}
}

// Pressing `r` twice in quick succession, before a fresh torrent has found
// peers, must not fabricate the two-reading run that means "dying". Only
// scheduled snapshots build the trend line; the newest ad-hoc one is the
// reading on screen, not a second data point.
func TestDyingIgnoresBurstOfManualChecks(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		manual([]SwarmProbe{{Hash: "a", Seeders: 0}}),
		manual([]SwarmProbe{{Hash: "a", Seeders: 0}}),
	}}
	if log.SwarmTrends()[0].Dying {
		t.Fatal("two manual re-checks seconds apart must not mark a swarm dying")
	}
}

// A manual check still counts as the latest reading, and its delta is measured
// against the last scheduled one rather than against another manual probe.
func TestSwarmDeltaSkipsManualSnapshots(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 10}}),
		manual([]SwarmProbe{{Hash: "a", Seeders: 6}}),
		manual([]SwarmProbe{{Hash: "a", Seeders: 4}}),
	}}
	tr := log.SwarmTrends()[0]
	if tr.Seeders != 4 {
		t.Errorf("seeders = %d, want the newest reading (4)", tr.Seeders)
	}
	if tr.Delta != -6 {
		t.Errorf("delta = %d, want -6 (against the daily, not the other manual)", tr.Delta)
	}
}

// A genuine seederless run across scheduled checks still reads as dying, even
// when a manual re-check sits on top of it.
func TestDyingSurvivesATrailingManualCheck(t *testing.T) {
	log := Log{Snapshots: []Snapshot{
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 0}}),
		daily(nil, []SwarmProbe{{Hash: "a", Seeders: 0}}),
		manual([]SwarmProbe{{Hash: "a", Seeders: 0}}),
	}}
	if !log.SwarmTrends()[0].Dying {
		t.Fatal("two seederless dailies must still read as dying")
	}
}
