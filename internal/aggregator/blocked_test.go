package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/provider"
)

func TestBlockedSourcePausesWithoutDelayingHealthySources(t *testing.T) {
	blocked := &fakeProvider{name: "blocked", failFor: 99, err: provider.ErrBlocked}
	healthy := &fakeProvider{name: "healthy", results: 1}
	a := New([]provider.Provider{blocked, healthy}, time.Second, 2)
	for i := 0; i < 3; i++ {
		rows, events := drain(a.Search(context.Background(), "query"))
		if len(rows) != 1 || rows[0].Provider != "healthy" {
			t.Fatal("blocked provider disrupted healthy results")
		}
		ev, ok := finalEvent(events, "blocked")
		if !ok || !errors.Is(ev.Err, provider.ErrBlocked) || !ev.RetryAt.After(time.Now()) {
			t.Fatalf("missing block recovery time: %+v", ev)
		}
	}
	if blocked.attempts.Load() != 1 || healthy.attempts.Load() != 3 {
		t.Fatal("subsequent searches hammered the blocked source or skipped a healthy one")
	}
	// Retry time is tested without sleeping or touching a public provider.
	a.blocks.until["blocked"] = time.Now().Add(-time.Second)
	drain(a.Search(context.Background(), "query"))
	if blocked.attempts.Load() != 2 {
		t.Fatal("expired pause did not retry automatically on the next search")
	}
	a.ClearBlocked("blocked")
	blocked.failFor = 0
	_, events := drain(a.Search(context.Background(), "query"))
	if ev, _ := finalEvent(events, "blocked"); ev.State != StateDone || !a.BlockedUntil("blocked").IsZero() {
		t.Fatal("manual recovery did not restore searches")
	}
}

func TestChangedSourceSettingsDoNotInheritPreviousBlock(t *testing.T) {
	p := &fakeProvider{name: "same-name"}
	a := New([]provider.Provider{p}, time.Second, 0)
	a.RecordBlocked(p.Name())
	next := a.WithProviders([]provider.Provider{p})
	if !next.BlockedUntil(p.Name()).IsZero() || a.BlockedUntil(p.Name()).IsZero() {
		t.Fatal("rebuilt pipeline inherited an old endpoint's pause or modified active searches")
	}
}
