package app

import (
	"context"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/config"
	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/store/inmemory"
)

func TestAdmin_CreateRule_UpdatesLocalCache_AndPublishesInvalidation(t *testing.T) {
	t.Parallel()

	app := newTestApplication(t)
	app.OutboxPublisher.SetInterval(20 * time.Millisecond)
	ctx := startTestApplication(t, app)

	_, err := app.AdminHandler.CreateRule(ctx, &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "create-1",
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	if _, ok := app.RuleCache.Get("tenant", "resource"); !ok {
		t.Fatalf("expected rule in cache")
	}

	handle, _, err := app.LimiterPool.Acquire(ctx, "tenant", "resource")
	if err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}
	limiter := handle.Limiter()
	handle.Release()

	waitForLimiterSwap(t, app.LimiterPool, "tenant", "resource", limiter, 500*time.Millisecond)
}

func TestAdmin_UpdateRule_TriggersCutoverAndNewLimiterVersion(t *testing.T) {
	t.Parallel()

	app := newTestApplication(t)
	app.OutboxPublisher.SetInterval(20 * time.Millisecond)
	ctx := startTestApplication(t, app)

	rule, err := app.AdminHandler.CreateRule(ctx, &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "update-1",
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	handle, _, err := app.LimiterPool.Acquire(ctx, "tenant", "resource")
	if err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}
	limiter := handle.Limiter()
	version := limiter.RuleVersion()
	handle.Release()

	updated, err := app.AdminHandler.UpdateRule(ctx, &core.UpdateRuleRequest{
		TenantID:        "tenant",
		Resource:        "resource",
		Algorithm:       "token_bucket",
		Limit:           15,
		Window:          time.Second,
		BurstSize:       0,
		ExpectedVersion: rule.Version,
	})
	if err != nil {
		t.Fatalf("unexpected update error: %v", err)
	}
	if updated.Version != rule.Version+1 {
		t.Fatalf("expected version %d got %d", rule.Version+1, updated.Version)
	}

	newLimiter := waitForLimiterVersion(t, app.LimiterPool, "tenant", "resource", updated.Version, 500*time.Millisecond)
	if newLimiter == limiter {
		t.Fatalf("expected new limiter after update")
	}
	if version >= newLimiter.RuleVersion() {
		t.Fatalf("expected limiter version to increase")
	}
}

func TestAdmin_DeleteRule_RemovesRuleFromCache(t *testing.T) {
	t.Parallel()

	app := newTestApplication(t)
	app.OutboxPublisher.SetInterval(20 * time.Millisecond)
	ctx := startTestApplication(t, app)

	rule, err := app.AdminHandler.CreateRule(ctx, &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "delete-1",
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	if err := app.AdminHandler.DeleteRule(ctx, "tenant", "resource", rule.Version); err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	waitForRuleMissing(t, app.RuleCache, "tenant", "resource", 500*time.Millisecond)

	resp, err := app.RateLimitHandler.CheckLimit(ctx, &core.CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("unexpected check error: %v", err)
	}
	if resp.ErrorCode != "RULE_NOT_FOUND" {
		t.Fatalf("expected RULE_NOT_FOUND got %q", resp.ErrorCode)
	}
	app.RateLimitHandler.ReleaseResponse(resp)
}

func TestFullSyncWorker_RepairsCacheAfterMissedInvalidation(t *testing.T) {
	t.Parallel()

	db := inmemory.NewInMemoryRuleDB(nil)
	rules := core.NewRuleCache()
	worker := core.NewCacheSyncWorker(db, rules, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	_, err := db.Create(context.Background(), &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "sync-1",
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	waitForRulePresent(t, rules, "tenant", "resource", 500*time.Millisecond)
}

func newTestApplication(t *testing.T) *Application {
	t.Helper()
	cfg := &config.Config{
		Region:            "test",
		RuleDB:            inmemory.NewInMemoryRuleDB(nil),
		Outbox:            inmemory.NewInMemoryOutbox(),
		PubSub:            inmemory.NewInMemoryPubSub(),
		Channel:           "ratelimit_invalidation",
		CacheSyncInterval: 50 * time.Millisecond,
		HealthInterval:    20 * time.Millisecond,
		LimiterPolicy: core.LimiterPolicy{
			Shards:        1,
			MaxEntriesShard: 4,
			QuiesceWindow: 10 * time.Millisecond,
			CloseTimeout:  100 * time.Millisecond,
		},
	}
	app, err := NewApplication(cfg)
	if err != nil {
		t.Fatalf("unexpected application error: %v", err)
	}
	return app
}

func startTestApplication(t *testing.T, app *Application) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		cancel()
		t.Fatalf("unexpected start error: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		_ = app.Shutdown(shutdownCtx)
		shutdownCancel()
	})
	return ctx
}

func waitForLimiterSwap(t *testing.T, pool *core.LimiterPool, tenantID, resource string, prev core.Limiter, timeout time.Duration) core.Limiter {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for limiter swap")
			return nil
		case <-time.After(10 * time.Millisecond):
			handle, _, err := pool.Acquire(context.Background(), tenantID, resource)
			if err != nil {
				continue
			}
			limiter := handle.Limiter()
			handle.Release()
			if limiter != prev {
				return limiter
			}
		}
	}
}

func waitForLimiterVersion(t *testing.T, pool *core.LimiterPool, tenantID, resource string, version int64, timeout time.Duration) core.Limiter {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for limiter version %d", version)
			return nil
		case <-time.After(10 * time.Millisecond):
			handle, _, err := pool.Acquire(context.Background(), tenantID, resource)
			if err != nil {
				continue
			}
			limiter := handle.Limiter()
			handle.Release()
			if limiter.RuleVersion() == version {
				return limiter
			}
		}
	}
}

func waitForRuleMissing(t *testing.T, cache *core.RuleCache, tenantID, resource string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for rule removal")
		case <-time.After(10 * time.Millisecond):
			if _, ok := cache.Get(tenantID, resource); !ok {
				return
			}
		}
	}
}

func waitForRulePresent(t *testing.T, cache *core.RuleCache, tenantID, resource string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for rule presence")
		case <-time.After(10 * time.Millisecond):
			if _, ok := cache.Get(tenantID, resource); ok {
				return
			}
		}
	}
}
