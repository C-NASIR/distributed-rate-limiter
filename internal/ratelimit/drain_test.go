package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestInFlight_Drains(t *testing.T) {
	t.Parallel()

	tracker := NewInFlight()
	if !tracker.Begin() {
		t.Fatalf("expected begin to succeed")
	}
	if !tracker.Begin() {
		t.Fatalf("expected begin to succeed")
	}
	tracker.End()
	tracker.End()
	tracker.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tracker.Wait(ctx); err != nil {
		t.Fatalf("expected drain to succeed: %v", err)
	}
}

func TestInFlight_ClosePreventsBegin(t *testing.T) {
	t.Parallel()

	tracker := NewInFlight()
	tracker.Close()
	if tracker.Begin() {
		t.Fatalf("expected begin to fail")
	}
}
