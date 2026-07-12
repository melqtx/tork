package autopilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/melqtx/tork/internal/provider"
)

func TestAppendDecisionWritesSmallLocalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autopilot.jsonl")
	plan := Plan{
		Picks:    []Pick{{Result: provider.Result{Title: "A release", Provider: "test", SizeBytes: 42, Seeders: 9}, Score: 12, Reason: "best available"}},
		Rejected: map[string]int{"over size limit": 2}, TotalBytes: 42, Outcome: "dry run",
	}
	if err := appendDecision(path, "find a thing", Intent{Query: "thing", MinSeeders: 5}, plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec decisionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("history is not JSONL: %v", err)
	}
	if rec.Outcome != "dry run" || len(rec.Picks) != 1 || rec.Picks[0].Title != "A release" {
		t.Fatalf("record = %+v", rec)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history mode = %v; want 0600", info.Mode().Perm())
	}
}
