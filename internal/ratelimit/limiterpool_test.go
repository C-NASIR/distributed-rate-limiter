package ratelimit

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type testLimiter struct {
	id         int64
	version    int64
	closeCount atomic.Int64
}

func (lim *testLimiter) Allow(ctx context.Context, key []byte, cost int64) (*Decision, error) {
	return &Decision{Allowed: true, Limit: 1, Remaining: 1}, nil
}

func (lim *testLimiter) AllowBatch(ctx context.Context, keys [][]byte, costs []int64) ([]*Decision, error) {
	decisions := make([]*Decision, len(keys))
	for i := range keys {
		decisions[i] = &Decision{Allowed: true, Limit: 1, Remaining: 1}
	}
	return decisions, nil
}

func (lim *testLimiter) RuleVersion() int64 {
	return lim.version
}

func (lim *testLimiter) Close() error {
	lim.closeCount.Add(1)
	return nil
}

func newTestFactory(counter *atomic.Int64) *LimiterFactory {
	return &LimiterFactory{CreateFunc: func(rule *Rule) (Limiter, RuleParams, error) {
		id := counter.Add(1)
		limiter := &testLimiter{id: id, version: rule.Version}
		params := RuleParams{
			Limit:   rule.Limit,
			Window:  rule.Window,
			Burst:   rule.BurstSize,
			Version: rule.Version,
		}
		return limiter, params, nil
	}}
}

func TestLimiterPool_AcquireAndRelease_RefCounting(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1}})

	var counter atomic.Int64
	pool := NewLimiterPool(rules, newTestFactory(&counter), LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   10 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	handle1, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}
	handle2, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire 2 failed: %v", err)
	}
	limiter1 := handle1.Limiter().(*testLimiter)
	if handle2.Limiter() != handle1.Limiter() {
		t.Fatalf("expected shared limiter")
	}

	handle1.Release()
	handle2.Release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pool.Cutover(ctx, "tenant-a", "resource-1", 2)

	waitForClose(t, limiter1, 300*time.Millisecond)
}

func TestLimiterPool_Cutover_ForcesNewLimiter(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1}})

	var counter atomic.Int64
	pool := NewLimiterPool(rules, newTestFactory(&counter), LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   5 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	handle, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	limiter1 := handle.Limiter().(*testLimiter)
	handle.Release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pool.Cutover(ctx, "tenant-a", "resource-1", 2)

	rules.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 2})

	handle2, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire after cutover failed: %v", err)
	}
	limiter2 := handle2.Limiter().(*testLimiter)
	if limiter2 == limiter1 {
		t.Fatalf("expected new limiter after cutover")
	}
	if limiter2.RuleVersion() != 2 {
		t.Fatalf("expected version 2 got %d", limiter2.RuleVersion())
	}
	handle2.Release()
}

func TestLimiterPool_QuiescingEntryNotHandedOut(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1}})

	var counter atomic.Int64
	pool := NewLimiterPool(rules, newTestFactory(&counter), LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   20 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	handle1, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	limiter1 := handle1.Limiter().(*testLimiter)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cutoverDone := make(chan struct{})
	go func() {
		pool.Cutover(ctx, "tenant-a", "resource-1", 2)
		close(cutoverDone)
	}()

	handle2 := waitForNewLimiter(t, pool, limiter1)
	handle1.Release()
	handle2.Release()

	<-cutoverDone
	waitForClose(t, limiter1, 300*time.Millisecond)
}

func TestLimiterPool_LRUEviction_RemovesOld(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1},
		{TenantID: "tenant-a", Resource: "resource-2", Limit: 10, Version: 1},
		{TenantID: "tenant-a", Resource: "resource-3", Limit: 10, Version: 1},
	})

	var counter atomic.Int64
	pool := NewLimiterPool(rules, newTestFactory(&counter), LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   5 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	h1, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}
	limiter1 := h1.Limiter().(*testLimiter)

	h2, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-2")
	if err != nil {
		t.Fatalf("acquire 2 failed: %v", err)
	}

	h3, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-3")
	if err != nil {
		t.Fatalf("acquire 3 failed: %v", err)
	}

	h4, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire 4 failed: %v", err)
	}
	limiter2 := h4.Limiter().(*testLimiter)
	if limiter2 == limiter1 {
		t.Fatalf("expected new limiter after eviction")
	}

	h1.Release()
	h2.Release()
	h3.Release()
	h4.Release()
}

func waitForClose(t *testing.T, limiter *testLimiter, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if limiter.closeCount.Load() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected limiter to be closed")
}

func waitForNewLimiter(t *testing.T, pool *LimiterPool, old *testLimiter) *LimiterHandle {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		handle, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
		if handle.Limiter() != old {
			return handle
		}
		handle.Release()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected new limiter")
	return nil
}
