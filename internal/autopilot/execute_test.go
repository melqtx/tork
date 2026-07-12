package autopilot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

type planProvider struct{ result provider.Result }

func (planProvider) Name() string { return "plan" }

func (p planProvider) Search(ctx context.Context, _ string, out chan<- provider.Result) error {
	select {
	case out <- p.result:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestExecuteDryRunExplainsAndRecordsWithoutQueueing(t *testing.T) {
	downloads := filepath.Join(t.TempDir(), "Downloads")
	t.Setenv("XDG_DOWNLOAD_DIR", downloads)
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	p := planProvider{provider.Result{
		Title: "Dune 2024 2160p WEB", Provider: "plan", Category: "Movies",
		Size: "7 GiB", SizeBytes: 7 << 30, Seeders: 100,
		Magnet: hashMagnet("abababababababababababababababababababab"),
	}}
	var out bytes.Buffer
	st := &state.State{}
	d := Deps{
		Cfg: cfg, Agg: aggregator.New([]provider.Provider{p}, cfg.SearchTimeout(), 1),
		State: st, Out: &out,
	}
	plan, err := d.Execute(context.Background(), "dune 2024 2160p under 8GB", Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != "dry run" || len(plan.Picks) != 1 || plan.Queued != 0 || len(st.Entries) != 0 {
		t.Fatalf("plan = %+v, state = %+v", plan, st)
	}
	for _, want := range []string{"max-size=8.0 GiB", "selected 1 download", "total: 7.0 GiB", "dry run - nothing queued"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output omitted %q:\n%s", want, out.String())
		}
	}
	data, err := os.ReadFile(cfg.AutopilotHistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"outcome":"dry run"`)) {
		t.Fatalf("history omitted dry-run outcome: %s", data)
	}
}

func TestExecuteCancelledPlanNeverTouchesEngine(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	p := planProvider{provider.Result{
		Title: "Safe Pick 1080p", Provider: "plan", SizeBytes: 1 << 30,
		Seeders: 100, Magnet: hashMagnet("cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"),
	}}
	st := &state.State{}
	d := Deps{
		Cfg: cfg, Agg: aggregator.New([]provider.Provider{p}, cfg.SearchTimeout(), 1),
		State: st, Out: &bytes.Buffer{}, Eng: nil,
	}
	plan, err := d.Execute(context.Background(), "safe pick 1080p", Options{Confirm: func(Plan) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != "cancelled" || plan.Queued != 0 || len(st.Entries) != 0 {
		t.Fatalf("cancelled plan = %+v, state = %+v", plan, st)
	}
}
