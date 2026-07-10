package health

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

// testConfig builds a real config rooted in a temp dir, so doctor's filesystem
// checks run against something it can actually stat.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func findCheck(t *testing.T, rep Report, name string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in report %+v", name, rep.Checks)
	return Check{}
}

func okEngine(*config.Config) (int, error) { return 51413, nil }

func TestRunDoctorHealthy(t *testing.T) {
	cfg := testConfig(t)
	store := Open(cfg.HealthPath())
	rep := RunDoctor(context.Background(), cfg,
		[]provider.Provider{fakeProvider{name: "good", results: 5}}, store, okEngine)

	if rep.Failed() {
		t.Fatalf("healthy setup reported a failure: %+v", rep.Checks)
	}
	if c := findCheck(t, rep, "engine"); !strings.Contains(c.Detail, "51413") {
		t.Errorf("engine check = %+v, want the listen port", c)
	}
	if c := findCheck(t, rep, "good"); c.Status != StatusOK || !strings.Contains(c.Detail, "5 results") {
		t.Errorf("provider check = %+v, want OK with a result count", c)
	}
	if c := findCheck(t, rep, "download dir"); c.Status != StatusOK {
		t.Errorf("download dir check = %+v, want OK", c)
	}
	if len(rep.Probe) != 1 {
		t.Errorf("report carries %d probes, want 1 for the caller to persist", len(rep.Probe))
	}
}

func TestDoctorMissingConfigExplainsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "good", results: 1}}, OpenReadOnly(cfg.HealthPath()), nil)
	if c := findCheck(t, rep, "config"); c.Status != StatusWarn || !strings.Contains(c.Detail, "defaults in memory") {
		t.Fatalf("missing config = %+v, want defaults-in-memory warning", c)
	}
}

func TestDoctorMissingDownloadDirFails(t *testing.T) {
	cfg := testConfig(t)
	cfg.SetDownloadDir(filepath.Join(t.TempDir(), "gone"))
	rep := RunDoctor(context.Background(), cfg, nil, Open(cfg.HealthPath()), okEngine)
	if c := findCheck(t, rep, "download dir"); c.Status != StatusFail {
		t.Fatalf("missing download dir = %+v, want a failure", c)
	}
	if !rep.Failed() {
		t.Fatal("a missing download dir must make the run fail")
	}
}

func TestDoctorEngineFailure(t *testing.T) {
	cfg := testConfig(t)
	bad := func(*config.Config) (int, error) { return 0, errors.New("address already in use") }
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "p"}}, Open(cfg.HealthPath()), bad)
	if c := findCheck(t, rep, "engine"); c.Status != StatusFail || !strings.Contains(c.Detail, "already in use") {
		t.Fatalf("engine check = %+v, want a failure carrying the reason", c)
	}
	if !rep.Failed() {
		t.Fatal("an engine that cannot start must make the run fail")
	}
}

// No providers means search can never return anything - a hard failure, not a
// warning.
func TestDoctorNoProvidersFails(t *testing.T) {
	cfg := testConfig(t)
	rep := RunDoctor(context.Background(), cfg, nil, Open(cfg.HealthPath()), okEngine)
	if c := findCheck(t, rep, "providers"); c.Status != StatusFail {
		t.Fatalf("no providers = %+v, want a failure", c)
	}
}

func TestDoctorAllProvidersDownFails(t *testing.T) {
	cfg := testConfig(t)
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{
		fakeProvider{name: "blocked", err: provider.ErrBlocked},
		fakeProvider{name: "dead", err: errors.New("no such host")},
	}, Open(cfg.HealthPath()), nil)
	if !rep.Failed() {
		t.Fatal("all unavailable providers must fail the doctor canary")
	}
	if c := findCheck(t, rep, "providers"); c.Status != StatusFail {
		t.Fatalf("provider aggregate = %+v, want failure", c)
	}
}

func TestDoctorLockedEngineWarns(t *testing.T) {
	cfg := testConfig(t)
	locked := func(*config.Config) (int, error) {
		return 0, errors.New("piece database is locked - another tork is already running")
	}
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "good", results: 1}}, Open(cfg.HealthPath()), locked)
	if c := findCheck(t, rep, "engine"); c.Status != StatusWarn || !strings.Contains(c.Detail, "another tork") {
		t.Fatalf("locked engine = %+v, want already-running warning", c)
	}
}

// A blocked or unreachable provider is a warning: the other sources still work.
func TestDoctorUnreachableProviderWarns(t *testing.T) {
	cfg := testConfig(t)
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{
		fakeProvider{name: "good", results: 1},
		fakeProvider{name: "blocked", err: provider.ErrBlocked},
		fakeProvider{name: "dead", err: errors.New("no such host")},
	}, Open(cfg.HealthPath()), okEngine)

	if c := findCheck(t, rep, "blocked"); c.Status != StatusWarn {
		t.Errorf("blocked provider = %+v, want a warning", c)
	}
	if c := findCheck(t, rep, "dead"); c.Status != StatusWarn || c.Detail != "unreachable (dns)" {
		t.Errorf("dead provider = %+v, want a warning with a calm reason", c)
	}
	if rep.Failed() {
		t.Error("unreachable providers must not fail the whole run")
	}
}

func TestDoctorCorruptStateFailsWithoutMutating(t *testing.T) {
	cfg := testConfig(t)
	corrupt := []byte("{{{ not json")
	if err := os.WriteFile(cfg.StatePath(), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "p"}}, Open(cfg.HealthPath()), okEngine)
	if c := findCheck(t, rep, "state"); c.Status != StatusFail {
		t.Fatalf("corrupt state = %+v, want a failure", c)
	}

	// Doctor diagnoses; it must never repair. The bad file stays exactly as it
	// was, and no .bak is left behind.
	got, err := os.ReadFile(cfg.StatePath())
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("doctor rewrote state.json: %q (%v)", got, err)
	}
	if _, err := os.Stat(cfg.StatePath() + ".bak"); err == nil {
		t.Fatal("doctor backed up state.json; it must not mutate the filesystem")
	}
}

func TestDoctorDefaultChecksDoNotWriteDownloadOrHealthFiles(t *testing.T) {
	cfg := testConfig(t)
	marker := filepath.Join(cfg.DownloadDir, "keep")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := OpenReadOnly(cfg.HealthPath())
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "good", results: 1}}, store, nil)
	if rep.Failed() {
		t.Fatalf("passive doctor unexpectedly failed: %+v", rep.Checks)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("doctor changed download data: %q (%v)", got, err)
	}
	if _, err := os.Stat(cfg.HealthPath()); !os.IsNotExist(err) {
		t.Fatalf("passive doctor wrote health history: %v", err)
	}
}

// An entry whose data vanished is worth flagging, but tork still runs.
func TestDoctorMissingDataWarns(t *testing.T) {
	cfg := testConfig(t)
	st := state.State{Entries: []state.Entry{
		{Magnet: "magnet:?x", Name: "gone", DataPath: filepath.Join(t.TempDir(), "vanished.mkv")},
	}}
	data, _ := json.Marshal(st)
	if err := os.WriteFile(cfg.StatePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "p"}}, Open(cfg.HealthPath()), okEngine)
	c := findCheck(t, rep, "state")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "1 with missing data") {
		t.Fatalf("state check = %+v, want a warning about the missing file", c)
	}
	if rep.Failed() {
		t.Error("a missing data file must not fail the run")
	}
}

// A long-silent automatic check means it never gets a chance to finish.
func TestDoctorStaleHistoryWarns(t *testing.T) {
	cfg := testConfig(t)
	cfg.Health.Enabled = true
	store := Open(cfg.HealthPath())
	if err := store.Append(Snapshot{At: time.Now().Add(-90 * time.Hour), Kind: KindDaily}); err != nil {
		t.Fatal(err)
	}
	rep := RunDoctor(context.Background(), cfg, []provider.Provider{fakeProvider{name: "p"}}, store, okEngine)
	if c := findCheck(t, rep, "health history"); c.Status != StatusWarn {
		t.Fatalf("stale history = %+v, want a warning", c)
	}
}

func TestFormatReportAligns(t *testing.T) {
	out := FormatReport(Report{Checks: []Check{
		{Name: "config", Status: StatusOK, Detail: "fine"},
		{Name: "download dir", Status: StatusFail, Detail: "gone"},
		{Name: "nyaa", Status: StatusWarn, Detail: "timed out"},
	}})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if !strings.HasPrefix(lines[0], "  ✓ config") {
		t.Errorf("ok line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  ✗ download dir") {
		t.Errorf("fail line = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "  ! nyaa") {
		t.Errorf("warn line = %q", lines[2])
	}
	// Details line up under each other regardless of name length.
	if strings.Index(lines[0], "fine") != strings.Index(lines[1], "gone") {
		t.Errorf("details are not aligned:\n%s", out)
	}
}
