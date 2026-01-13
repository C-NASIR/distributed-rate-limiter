package ratelimit

import (
	"testing"
	"time"
)

func TestCircuitBreaker_OpensAndRecovers(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(CircuitOptions{FailureThreshold: 2, OpenDuration: 30 * time.Millisecond, HalfOpenMaxCalls: 1})
	if !cb.Allow() {
		t.Fatalf("expected allow in closed state")
	}
	cb.OnFailure()
	cb.OnFailure()
	if cb.Allow() {
		t.Fatalf("expected breaker to be open")
	}
	time.Sleep(35 * time.Millisecond)
	if !cb.Allow() {
		t.Fatalf("expected breaker to allow in half-open")
	}
	cb.OnSuccess()
	if !cb.Allow() {
		t.Fatalf("expected breaker to close after success")
	}
}
