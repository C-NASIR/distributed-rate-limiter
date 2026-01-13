// Package ratelimit provides in flight tracking for graceful drains.
package ratelimit

import (
	"context"
	"sync/atomic"
)

// InFlight tracks in flight requests.
type InFlight struct {
	n      atomic.Int64
	closed atomic.Bool
	ch     chan struct{}
}

// NewInFlight constructs a new InFlight tracker.
func NewInFlight() *InFlight {
	return &InFlight{ch: make(chan struct{})}
}

// Begin registers a new in flight request.
func (f *InFlight) Begin() bool {
	if f == nil {
		return false
	}
	if f.closed.Load() {
		return false
	}
	f.n.Add(1)
	if f.closed.Load() {
		f.n.Add(-1)
		return false
	}
	return true
}

// End marks a request as complete.
func (f *InFlight) End() {
	if f == nil {
		return
	}
	if f.n.Add(-1) == 0 && f.closed.Load() {
		close(f.ch)
	}
}

// Close prevents new requests and waits for drain.
func (f *InFlight) Close() {
	if f == nil {
		return
	}
	if !f.closed.CompareAndSwap(false, true) {
		return
	}
	if f.n.Load() == 0 {
		close(f.ch)
	}
}

// Wait blocks until drained or context done.
func (f *InFlight) Wait(ctx context.Context) error {
	if f == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-f.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
