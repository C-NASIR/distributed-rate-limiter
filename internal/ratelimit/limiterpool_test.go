package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLimiterPool_AcquireAndRelease_RefCounting(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1}})

	pool := newTestLimiterPool(rules, LimiterPolicy{
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
	limiter1 := handle1.Limiter()
	if handle2.Limiter() != handle1.Limiter() {
		t.Fatalf("expected shared limiter")
	}

	handle1.Release()
	handle2.Release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pool.Cutover(ctx, "tenant-a", "resource-1", 2)

	handle3, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire after cutover failed: %v", err)
	}
	if handle3.Limiter() == limiter1 {
		t.Fatalf("expected new limiter after cutover")
	}
	handle3.Release()
}

func TestLimiterPool_Cutover_ForcesNewLimiter(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1}})

	pool := newTestLimiterPool(rules, LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   5 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	handle, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	limiter1 := handle.Limiter()
	handle.Release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pool.Cutover(ctx, "tenant-a", "resource-1", 2)

	rules.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 2})

	handle2, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire after cutover failed: %v", err)
	}
	limiter2 := handle2.Limiter()
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

	pool := newTestLimiterPool(rules, LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   20 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	handle1, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	limiter1 := handle1.Limiter()

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
}

func TestLimiterPool_LRUEviction_RemovesOld(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1},
		{TenantID: "tenant-a", Resource: "resource-2", Limit: 10, Version: 1},
		{TenantID: "tenant-a", Resource: "resource-3", Limit: 10, Version: 1},
	})

	pool := newTestLimiterPool(rules, LimiterPolicy{
		Shards:          1,
		MaxEntriesShard: 2,
		QuiesceWindow:   5 * time.Millisecond,
		CloseTimeout:    200 * time.Millisecond,
	})

	h1, _, err := pool.Acquire(context.Background(), "tenant-a", "resource-1")
	if err != nil {
		t.Fatalf("acquire 1 failed: %v", err)
	}
	limiter1 := h1.Limiter()

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
	limiter2 := h4.Limiter()
	if limiter2 == limiter1 {
		t.Fatalf("expected new limiter after eviction")
	}

	h1.Release()
	h2.Release()
	h3.Release()
	h4.Release()
}

func waitForNewLimiter(t *testing.T, pool *LimiterPool, old Limiter) *LimiterHandle {
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

func newTestLimiterPool(rules *RuleCache, policy LimiterPolicy) *LimiterPool {
	redis := NewInMemoryRedis(nil)
	membership := NewSingleInstanceMembership("self", "region")
	degrade := NewDegradeController(redis, membership, DegradeThresholds{}, "region", false, 0)
	fallback := &FallbackLimiter{
		ownership:   NewRendezvousOwnership(membership, "region", false, true),
		mship:       membership,
		region:      "region",
		regionGroup: "region",
		policy:      normalizeFallbackPolicy(FallbackPolicy{}),
		mode:        degrade,
		local:       &LocalLimiterStore{},
	}
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: degrade}
	return NewLimiterPool(rules, factory, policy)
}
