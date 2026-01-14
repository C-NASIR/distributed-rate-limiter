package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescer_SharesSingleExecution(t *testing.T) {
	t.Parallel()

	coalescer := NewCoalescer(1, 50*time.Millisecond)
	var count atomic.Int64
	fn := func() (*Decision, error) {
		count.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &Decision{Allowed: true, Remaining: 1, Limit: 10}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := coalescer.Do(context.Background(), "tenant\x1fuser\x1fresource\x1f1", fn)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if decision == nil || !decision.Allowed {
				t.Errorf("unexpected decision: %#v", decision)
			}
		}()
	}

	wg.Wait()
	if got := count.Load(); got != 1 {
		t.Fatalf("expected 1 execution got %d", got)
	}
}

func TestCoalescer_Expires(t *testing.T) {
	t.Parallel()

	coalescer := NewCoalescer(1, 20*time.Millisecond)
	var count atomic.Int64
	fn := func() (*Decision, error) {
		count.Add(1)
		return &Decision{Allowed: true}, nil
	}

	if _, err := coalescer.Do(context.Background(), "key", fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := coalescer.Do(context.Background(), "key", fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 executions got %d", got)
	}
}
