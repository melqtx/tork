package aggregator

import (
	"sync"
	"time"
)

const blockedCooldown = 10 * time.Minute

// Pauses are local to this pipeline and never change saved source choices.
// Health probes bypass the pipeline, allowing an explicit check to recover it.
type blockedSources struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newBlockedSources() *blockedSources {
	return &blockedSources{until: make(map[string]time.Time)}
}

func (a *Aggregator) BlockedUntil(name string) time.Time {
	if a == nil || a.blocks == nil {
		return time.Time{}
	}
	a.blocks.mu.Lock()
	defer a.blocks.mu.Unlock()
	until := a.blocks.until[name]
	if !until.After(time.Now()) {
		delete(a.blocks.until, name)
		return time.Time{}
	}
	return until
}

func (a *Aggregator) RecordBlocked(name string) time.Time {
	if a == nil || a.blocks == nil {
		return time.Time{}
	}
	a.blocks.mu.Lock()
	defer a.blocks.mu.Unlock()
	until := time.Now().Add(blockedCooldown)
	a.blocks.until[name] = until
	return until
}

func (a *Aggregator) ClearBlocked(name string) {
	if a == nil || a.blocks == nil {
		return
	}
	a.blocks.mu.Lock()
	defer a.blocks.mu.Unlock()
	delete(a.blocks.until, name)
}
