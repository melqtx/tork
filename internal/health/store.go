// Package health records how tork's world is doing over time: whether each
// search provider still answers, and whether the swarms behind the library
// still have peers. Snapshots accumulate in ~/.tork/health.json - one per
// automatic daily check - and the TUI's compass screen reads trends out of
// them. The doctor command runs the same probes on demand.
package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxSnapshots bounds health.json. At one automatic check a day this is a
// three-month window, which is long enough to see a provider rot and short
// enough that the file stays a few dozen KB.
const maxSnapshots = 90

// Kind separates the scheduled checks that build history from the ad-hoc ones
// a doctor run produces, so trends aren't skewed by someone running `tork
// doctor` in a loop.
const (
	KindDaily  = "daily"
	KindDoctor = "doctor"
	KindManual = "manual"
)

// ProviderProbe is one provider's answer to a canary search. OK means the
// provider was reachable and replied - zero results is still OK, since a
// canary query legitimately matches nothing on some providers.
type ProviderProbe struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Blocked   bool   `json:"blocked,omitempty"`
	Err       string `json:"err,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Results   int    `json:"results"`
}

// SwarmProbe is one library item's live swarm at a moment in time. Seeders
// counts connected complete peers, not a tracker's scrape total.
type SwarmProbe struct {
	Hash    string `json:"hash"`
	Name    string `json:"name"`
	Seeders int    `json:"seeders"`
	Active  int    `json:"active"`
	Peers   int    `json:"peers"`
	Done    bool   `json:"done"`
}

// Snapshot is one full round of checks.
type Snapshot struct {
	At        time.Time       `json:"at"`
	Kind      string          `json:"kind"`
	Providers []ProviderProbe `json:"providers,omitempty"`
	Swarms    []SwarmProbe    `json:"swarms,omitempty"`
}

// Log is the persisted history, oldest first.
type Log struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// Store is a Log plus its file, guarding concurrent access from the TUI's
// refresh command and the background check that runs at launch.
type Store struct {
	path    string
	mu      sync.Mutex
	log     Log
	loadErr error
}

// Open reads the health log. Like the resume state, a missing, unreadable, or
// corrupt file yields an empty log rather than an error - losing health
// history is a nuisance, failing to start is not. A corrupt file is preserved
// as .bak.
func Open(path string) *Store {
	s := OpenReadOnly(path)
	if s.loadErr == nil {
		return s
	}
	if errors.Is(s.loadErr, errCorruptHistory) {
		_ = os.Rename(path, path+".bak")
		fmt.Fprintf(os.Stderr, "tork: %s was corrupt (%v); backed up to %s.bak\n", path, s.loadErr, path)
		s.log = Log{}
		s.loadErr = nil
		return s
	}
	fmt.Fprintf(os.Stderr, "tork: could not read %s (%v); starting with an empty health history\n", path, s.loadErr)
	s.loadErr = nil
	return s
}

var errCorruptHistory = errors.New("corrupt health history")

// OpenReadOnly loads history without renaming or repairing a broken file.
// Callers can inspect LoadError and decide how to surface it.
func OpenReadOnly(path string) *Store {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.loadErr = err
		}
		return s
	}
	if err := json.Unmarshal(data, &s.log); err != nil {
		s.loadErr = fmt.Errorf("%w: %v", errCorruptHistory, err)
		s.log = Log{}
	}
	return s
}

// Append records a snapshot, trimming the oldest once the cap is reached, and
// persists the log. The parent directory is created up front: withFileLock
// needs to open a lock file beside health.json, which `tork doctor --record`
// may reach before anything has created ~/.tork.
func (s *Store) Append(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return withFileLock(s.path, func() error {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		s.log.Snapshots = append(s.log.Snapshots, snap)
		if n := len(s.log.Snapshots); n > maxSnapshots {
			s.log.Snapshots = append([]Snapshot(nil), s.log.Snapshots[n-maxSnapshots:]...)
		}
		return s.saveLocked()
	})
}

// saveLocked writes atomically via temp file + rename. Callers hold both the
// mutex and the file lock, and must have reloaded first: this clobbers whatever
// is on disk with the in-memory log.
func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.log, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".health-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// reloadLocked folds in a snapshot written by another tork process while we
// held the advisory lock, so append never writes over newer on-disk history.
func (s *Store) reloadLocked() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.log = Log{}
		return nil
	}
	if err != nil {
		return err
	}
	var log Log
	if err := json.Unmarshal(data, &log); err != nil {
		return fmt.Errorf("%w: %v", errCorruptHistory, err)
	}
	s.log = log
	return nil
}

// Log returns a copy of the history, safe to read while a check is running.
func (s *Store) Log() Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Log{Snapshots: append([]Snapshot(nil), s.log.Snapshots...)}
}

// LoadError reports a passive-load problem. It is primarily for doctor, which
// must report corrupt history without repairing it.
func (s *Store) LoadError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr
}

// LastDaily reports when the last scheduled check ran. Doctor runs don't count:
// they are on-demand, and letting them reset the clock would mean a user who
// runs doctor never gets a real daily datapoint.
func (s *Store) LastDaily() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.log.Snapshots) - 1; i >= 0; i-- {
		if s.log.Snapshots[i].Kind == KindDaily {
			return s.log.Snapshots[i].At, true
		}
	}
	return time.Time{}, false
}

// Due reports whether a scheduled check should run now.
func (s *Store) Due(interval time.Duration) bool {
	last, ok := s.LastDaily()
	if !ok {
		return true
	}
	return time.Since(last) >= interval
}
