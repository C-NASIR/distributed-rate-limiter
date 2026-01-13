// Package ratelimit provides a circuit breaker.
package ratelimit

import (
	"sync/atomic"
	"time"
)

// CircuitState represents breaker state.
type CircuitState int32

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// CircuitOptions configures breaker thresholds.
type CircuitOptions struct {
	FailureThreshold int64
	OpenDuration     time.Duration
	HalfOpenMaxCalls int64
}

// CircuitBreaker tracks failures and controls access.
type CircuitBreaker struct {
	state           atomic.Int32
	lastFailure     atomic.Int64
	openUntil       atomic.Int64
	failures        atomic.Int64
	halfOpenInFlight atomic.Int64
	opts            CircuitOptions
}

// NewCircuitBreaker constructs a breaker with defaults.
func NewCircuitBreaker(opts CircuitOptions) *CircuitBreaker {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 10
	}
	if opts.OpenDuration <= 0 {
		opts.OpenDuration = 200 * time.Millisecond
	}
	if opts.HalfOpenMaxCalls <= 0 {
		opts.HalfOpenMaxCalls = 5
	}
	cb := &CircuitBreaker{opts: opts}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// Allow reports whether the call should proceed.
func (cb *CircuitBreaker) Allow() bool {
	if cb == nil {
		return true
	}
	state := CircuitState(cb.state.Load())
	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Now().UnixNano() >= cb.openUntil.Load() {
			cb.state.Store(int32(CircuitHalfOpen))
			cb.halfOpenInFlight.Store(0)
			return true
		}
		return false
	case CircuitHalfOpen:
		inFlight := cb.halfOpenInFlight.Add(1)
		if inFlight <= cb.opts.HalfOpenMaxCalls {
			return true
		}
		cb.halfOpenInFlight.Add(-1)
		return false
	default:
		return true
	}
}

// OnSuccess records a successful call.
func (cb *CircuitBreaker) OnSuccess() {
	if cb == nil {
		return
	}
	state := CircuitState(cb.state.Load())
	if state == CircuitHalfOpen {
		cb.halfOpenInFlight.Add(-1)
		cb.failures.Store(0)
		cb.state.Store(int32(CircuitClosed))
		return
	}
	if state == CircuitClosed {
		cb.failures.Store(0)
	}
}

// OnFailure records a failure and updates state.
func (cb *CircuitBreaker) OnFailure() {
	if cb == nil {
		return
	}
	now := time.Now().UnixNano()
	cb.lastFailure.Store(now)
	state := CircuitState(cb.state.Load())
	if state == CircuitHalfOpen {
		cb.halfOpenInFlight.Add(-1)
		cb.failures.Store(cb.opts.FailureThreshold)
		cb.openUntil.Store(time.Now().Add(cb.opts.OpenDuration).UnixNano())
		cb.state.Store(int32(CircuitOpen))
		return
	}
	failures := cb.failures.Add(1)
	if failures >= cb.opts.FailureThreshold {
		cb.openUntil.Store(time.Now().Add(cb.opts.OpenDuration).UnixNano())
		cb.state.Store(int32(CircuitOpen))
	}
}
