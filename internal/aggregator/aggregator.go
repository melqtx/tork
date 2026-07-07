// Package aggregator fans a search out to all providers concurrently and
// fans results back into a single stream.
package aggregator

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/melqtx/tork/internal/provider"
)

type ProviderState int

const (
	StateSearching ProviderState = iota
	StateDone
	StateFailed
)

type StatusEvent struct {
	Provider string
	State    ProviderState
	Err      error // set when StateFailed
	Count    int   // results emitted so far by this provider
}

type Aggregator struct {
	providers []provider.Provider
	timeout   time.Duration // per attempt
	retries   int
}

func New(providers []provider.Provider, timeout time.Duration, retries int) *Aggregator {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Aggregator{providers: providers, timeout: timeout, retries: retries}
}

func (a *Aggregator) Providers() []provider.Provider { return a.providers }

// Search fans out to all providers. Both returned channels are closed by the
// aggregator once every provider has finished or ctx is cancelled.
func (a *Aggregator) Search(ctx context.Context, query string) (<-chan provider.Result, <-chan StatusEvent) {
	results := make(chan provider.Result, 64)
	status := make(chan StatusEvent, len(a.providers)*2)

	var wg sync.WaitGroup
	for _, p := range a.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.searchOne(ctx, p, query, results, status)
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		close(status)
	}()
	return results, status
}

func (a *Aggregator) searchOne(ctx context.Context, p provider.Provider, query string, results chan<- provider.Result, status chan<- StatusEvent) {
	sendStatus := func(ev StatusEvent) {
		select {
		case status <- ev:
		case <-ctx.Done():
		}
	}
	sendStatus(StatusEvent{Provider: p.Name(), State: StateSearching})

	// The provider writes into a proxy channel; a forwarder counts rows and
	// pushes them onto the shared results channel. The count feeds the
	// StateDone/StateFailed events shown in the UI.
	count := 0
	var err error
	for attempt := 0; attempt <= a.retries; attempt++ {
		proxy := make(chan provider.Result, 16)
		forwarded := make(chan struct{})
		go func() {
			defer close(forwarded)
			for r := range proxy {
				select {
				case results <- r:
					count++
				case <-ctx.Done():
					// keep draining so the provider's sends never block
				}
			}
		}()

		attemptCtx, cancel := context.WithTimeout(ctx, a.timeout)
		err = safeSearch(attemptCtx, p, query, proxy)
		cancel()
		close(proxy)
		<-forwarded

		if err == nil {
			sendStatus(StatusEvent{Provider: p.Name(), State: StateDone, Count: count})
			return
		}
		// parent cancelled: quit silently; blocked: retrying is pointless
		if ctx.Err() != nil || errors.Is(err, provider.ErrBlocked) {
			break
		}
		if attempt < a.retries {
			backoff := time.Duration(1<<attempt)*500*time.Millisecond +
				time.Duration(rand.IntN(200))*time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
		}
	}
	if ctx.Err() != nil {
		return // cancelled searches report nothing
	}
	sendStatus(StatusEvent{Provider: p.Name(), State: StateFailed, Err: err, Count: count})
}

// safeSearch runs a provider search, converting any panic into an error so a
// single misbehaving provider can never crash the program.
func safeSearch(ctx context.Context, p provider.Provider, query string, out chan<- provider.Result) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", p.Name(), r)
		}
	}()
	return p.Search(ctx, query, out)
}
