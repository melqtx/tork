package health

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	s := Open(path)
	snap := Snapshot{
		At:        time.Now().UTC().Truncate(time.Second),
		Kind:      KindDaily,
		Providers: []ProviderProbe{{Name: "knaben", OK: true, LatencyMS: 120, Results: 50}},
		Swarms:    []SwarmProbe{{Hash: "abc", Name: "movie", Seeders: 3, Peers: 9}},
	}
	if err := s.Append(snap); err != nil {
		t.Fatalf("append: %v", err)
	}

	got := Open(path).Log()
	if len(got.Snapshots) != 1 {
		t.Fatalf("reloaded %d snapshots, want 1", len(got.Snapshots))
	}
	if p := got.Snapshots[0].Providers; len(p) != 1 || p[0].Name != "knaben" || p[0].LatencyMS != 120 {
		t.Fatalf("provider probe did not survive the round trip: %+v", p)
	}
	if w := got.Snapshots[0].Swarms; len(w) != 1 || w[0].Seeders != 3 {
		t.Fatalf("swarm probe did not survive the round trip: %+v", w)
	}
}

func TestOpenReadOnlyLeavesCorruptHistoryUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	bad := []byte("{{{ bad")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	s := OpenReadOnly(path)
	if s.LoadError() == nil {
		t.Fatal("corrupt passive history should report an error")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("passive open changed history: %q (%v)", got, err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Fatal("passive open created a backup")
	}
}

func TestConcurrentStoresRetainBothSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	a, b := Open(path), Open(path)
	var wg sync.WaitGroup
	for _, item := range []struct {
		store *Store
		name  string
	}{{a, "a"}, {b, "b"}} {
		wg.Add(1)
		go func(store *Store, name string) {
			defer wg.Done()
			if err := store.Append(Snapshot{Kind: KindDaily, Providers: []ProviderProbe{{Name: name}}}); err != nil {
				t.Errorf("append %s: %v", name, err)
			}
		}(item.store, item.name)
	}
	wg.Wait()
	if got := Open(path).Log().Snapshots; len(got) != 2 {
		t.Fatalf("persisted snapshots = %d, want both writes", len(got))
	}
}

// A missing file is the first-run case, not an error.
func TestOpenMissingFile(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "nope.json"))
	if n := len(s.Log().Snapshots); n != 0 {
		t.Fatalf("fresh store has %d snapshots, want 0", n)
	}
	if !s.Due(time.Hour) {
		t.Fatal("a store that has never checked must be due")
	}
}

// Corrupt history is preserved as .bak and the app carries on, mirroring how
// state.json is handled.
func TestOpenCorruptFileBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	if err := os.WriteFile(path, []byte("{{{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Open(path)
	if n := len(s.Log().Snapshots); n != 0 {
		t.Fatalf("corrupt store yielded %d snapshots, want 0", n)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("corrupt file was not backed up: %v", err)
	}
}

// The log is bounded so it can't grow without limit.
func TestAppendCapsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	s := Open(path)
	for i := 0; i < maxSnapshots+10; i++ {
		snap := Snapshot{Kind: KindDaily, Providers: []ProviderProbe{{Name: "p", Results: i}}}
		if err := s.Append(snap); err != nil {
			t.Fatal(err)
		}
	}
	log := s.Log()
	if len(log.Snapshots) != maxSnapshots {
		t.Fatalf("history holds %d snapshots, want the cap %d", len(log.Snapshots), maxSnapshots)
	}
	// The oldest must be dropped, not the newest.
	if got := log.Snapshots[len(log.Snapshots)-1].Providers[0].Results; got != maxSnapshots+9 {
		t.Fatalf("newest snapshot has Results=%d, want the last appended (%d)", got, maxSnapshots+9)
	}
	if got := log.Snapshots[0].Providers[0].Results; got != 10 {
		t.Fatalf("oldest retained snapshot has Results=%d, want 10", got)
	}
}

// A doctor run is on-demand and must not reset the daily clock, otherwise
// someone debugging in a loop would never record a real daily datapoint.
func TestDueIgnoresDoctorRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	s := Open(path)

	old := time.Now().Add(-48 * time.Hour)
	if err := s.Append(Snapshot{At: old, Kind: KindDaily}); err != nil {
		t.Fatal(err)
	}
	if !s.Due(24 * time.Hour) {
		t.Fatal("a 48h-old daily check must be due at a 24h interval")
	}

	if err := s.Append(Snapshot{At: time.Now(), Kind: KindDoctor}); err != nil {
		t.Fatal(err)
	}
	if !s.Due(24 * time.Hour) {
		t.Fatal("a fresh doctor run must not satisfy the daily check")
	}

	if err := s.Append(Snapshot{At: time.Now(), Kind: KindDaily}); err != nil {
		t.Fatal(err)
	}
	if s.Due(24 * time.Hour) {
		t.Fatal("a fresh daily check must clear the due flag")
	}
}

// `tork doctor --record` can reach Append before anything has created ~/.tork,
// and the advisory lock file lives in that directory.
func TestAppendCreatesMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tork", "health.json")
	if err := Open(path).Append(Snapshot{At: time.Now().UTC(), Kind: KindDoctor}); err != nil {
		t.Fatalf("append into a missing directory: %v", err)
	}
	if len(Open(path).Log().Snapshots) != 1 {
		t.Fatal("snapshot did not survive")
	}
}
