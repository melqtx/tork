package aggregator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/provider"
)

// fakeProvider emits n results, optionally failing or hanging.
type fakeProvider struct {
	name     string
	results  int
	failFor  int32 // fail this many attempts before succeeding
	err      error // error to return while failing
	hang     bool  // block until ctx is done
	attempts atomic.Int32
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Search(ctx context.Context, query string, out chan<- provider.Result) error {
	attempt := f.attempts.Add(1)
	if f.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if attempt <= f.failFor {
		return f.err
	}
	for i := 0; i < f.results; i++ {
		select {
		case out <- provider.Result{Title: f.name, Provider: f.name}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func drain(results <-chan provider.Result, status <-chan StatusEvent) ([]provider.Result, []StatusEvent) {
	var rs []provider.Result
	var evs []StatusEvent
	for results != nil || status != nil {
		select {
		case r, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			rs = append(rs, r)
		case ev, ok := <-status:
			if !ok {
				status = nil
				continue
			}
			evs = append(evs, ev)
		}
	}
	return rs, evs
}

func finalEvent(evs []StatusEvent, name string) (StatusEvent, bool) {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Provider == name {
			return evs[i], true
		}
	}
	return StatusEvent{}, false
}

func TestFanInAllProviders(t *testing.T) {
	a := New([]provider.Provider{
		&fakeProvider{name: "a", results: 3},
		&fakeProvider{name: "b", results: 2},
	}, time.Second, 2)
	rs, evs := drain(a.Search(context.Background(), "q"))
	if len(rs) != 5 {
		t.Errorf("got %d results, want 5", len(rs))
	}
	for _, name := range []string{"a", "b"} {
		ev, ok := finalEvent(evs, name)
		if !ok || ev.State != StateDone {
			t.Errorf("provider %s final event = %+v", name, ev)
		}
	}
	if ev, _ := finalEvent(evs, "a"); ev.Count != 3 {
		t.Errorf("a count = %d, want 3", ev.Count)
	}
}

func TestRetryThenSucceed(t *testing.T) {
	p := &fakeProvider{name: "flaky", results: 1, failFor: 2, err: errors.New("boom")}
	a := New([]provider.Provider{p}, time.Second, 2)
	rs, evs := drain(a.Search(context.Background(), "q"))
	if len(rs) != 1 {
		t.Errorf("got %d results, want 1", len(rs))
	}
	if got := p.attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	if ev, _ := finalEvent(evs, "flaky"); ev.State != StateDone {
		t.Errorf("final state = %+v", ev)
	}
}

func TestFailAfterRetriesExhausted(t *testing.T) {
	p := &fakeProvider{name: "dead", failFor: 99, err: errors.New("boom")}
	a := New([]provider.Provider{p}, time.Second, 1)
	_, evs := drain(a.Search(context.Background(), "q"))
	if got := p.attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (1 + 1 retry)", got)
	}
	ev, _ := finalEvent(evs, "dead")
	if ev.State != StateFailed || ev.Err == nil {
		t.Errorf("final event = %+v", ev)
	}
}

func TestBlockedIsNotRetried(t *testing.T) {
	p := &fakeProvider{name: "cf", failFor: 99, err: provider.ErrBlocked}
	a := New([]provider.Provider{p}, time.Second, 2)
	_, evs := drain(a.Search(context.Background(), "q"))
	if got := p.attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retries for ErrBlocked)", got)
	}
	if ev, _ := finalEvent(evs, "cf"); ev.State != StateFailed {
		t.Errorf("final event = %+v", ev)
	}
}

func TestTimeoutRetries(t *testing.T) {
	p := &fakeProvider{name: "slow", hang: true}
	a := New([]provider.Provider{p}, 50*time.Millisecond, 1)
	start := time.Now()
	_, evs := drain(a.Search(context.Background(), "q"))
	if got := p.attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (timeout is retryable)", got)
	}
	if ev, _ := finalEvent(evs, "slow"); ev.State != StateFailed {
		t.Errorf("final event = %+v", ev)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
}

func TestCancellationClosesChannels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New([]provider.Provider{
		&fakeProvider{name: "hang", hang: true},
		&fakeProvider{name: "ok", results: 100},
	}, time.Minute, 0)
	results, status := a.Search(ctx, "q")

	// read a couple of results then cancel mid-stream
	<-results
	cancel()

	done := make(chan struct{})
	go func() {
		drain(results, status)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channels never closed after cancellation")
	}
}

// panicProvider always panics - it must be contained, not crash the program.
type panicProvider struct{ name string }

func (p *panicProvider) Name() string { return p.name }
func (p *panicProvider) Search(context.Context, string, chan<- provider.Result) error {
	panic("boom from provider")
}

func TestPanickingProviderIsContained(t *testing.T) {
	a := New([]provider.Provider{
		&panicProvider{name: "bad"},
		&fakeProvider{name: "good", results: 3},
	}, time.Second, 1)
	rs, evs := drain(a.Search(context.Background(), "q"))
	// the good provider still delivers
	if len(rs) != 3 {
		t.Errorf("good provider results = %d, want 3", len(rs))
	}
	// the bad provider is reported failed, not fatal
	ev, ok := finalEvent(evs, "bad")
	if !ok || ev.State != StateFailed || ev.Err == nil {
		t.Errorf("panicking provider final event = %+v", ev)
	}
}
