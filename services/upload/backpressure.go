// services/upload/backpressure.go

package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// InflightGauge admits chunk bodies against a configurable inflight
// bytes ceiling. When the box is saturated the service answers 429 with
// a server directed backoff hint instead of buffering more data.
type InflightGauge struct {
	ceiling int64
	current atomic.Int64
}

// NewInflightGauge builds a gauge with the given byte ceiling.
func NewInflightGauge(ceiling int64) *InflightGauge {
	return &InflightGauge{ceiling: ceiling}
}

// TryAcquire admits n bytes if the ceiling allows it.
func (g *InflightGauge) TryAcquire(n int64) bool {
	for {
		cur := g.current.Load()
		if cur+n > g.ceiling {
			return false
		}
		if g.current.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

// Release returns n bytes to the gauge.
func (g *InflightGauge) Release(n int64) {
	g.current.Add(-n)
}

// Current reports the admitted byte count (for logs and tests).
func (g *InflightGauge) Current() int64 {
	return g.current.Load()
}

// BackoffDirector tracks a server directed backoff level per session.
// Every saturated denial escalates the session's retry delay up to a
// cap; a successful admission resets it.
type BackoffDirector struct {
	mu     sync.Mutex
	levels map[uuid.UUID]int
	base   time.Duration
	max    time.Duration
}

// NewBackoffDirector builds a director with base delay and cap.
func NewBackoffDirector(base, max time.Duration) *BackoffDirector {
	return &BackoffDirector{
		levels: make(map[uuid.UUID]int),
		base:   base,
		max:    max,
	}
}

// Deny escalates the session's backoff level and returns the delay the
// client must wait before retrying.
func (b *BackoffDirector) Deny(id uuid.UUID) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	level := b.levels[id]
	b.levels[id] = level + 1
	d := b.base << uint(level)
	if d > b.max || d <= 0 {
		d = b.max
	}
	return d
}

// Reset clears the session's backoff level after a successful admit.
func (b *BackoffDirector) Reset(id uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.levels, id)
}
