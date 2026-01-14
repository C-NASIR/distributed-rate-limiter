package core_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/core"
)

func TestRendezvousOwnership_StableOwner(t *testing.T) {
	t.Parallel()

	membership := core.NewStaticMembership("node-a", "region-a", []core.InstanceInfo{
		{ID: "node-a", Region: "region-a", Weight: 1},
		{ID: "node-b", Region: "region-a", Weight: 1},
		{ID: "node-c", Region: "region-a", Weight: 1},
	})
	owner := core.NewRendezvousOwnership(membership, "region-a", false, true)
	key := []byte("stable-key")

	first := owner.IsOwner(context.Background(), key)
	for i := 0; i < 5; i++ {
		if owner.IsOwner(context.Background(), key) != first {
			t.Fatalf("expected stable ownership result")
		}
	}
}

func TestFallbackLimiter_DenyWhenNotOwner(t *testing.T) {
	t.Parallel()

	membership := core.NewStaticMembership("node-a", "region-a", []core.InstanceInfo{
		{ID: "node-a", Region: "region-a", Weight: 1},
		{ID: "node-b", Region: "region-a", Weight: 1},
	})
	owner := core.NewRendezvousOwnership(membership, "region-a", false, true)
	var key []byte
	for i := 0; i < 100; i++ {
		candidate := []byte(fmt.Sprintf("key-%d", i))
		if !owner.IsOwner(context.Background(), candidate) {
			key = candidate
			break
		}
	}
	if key == nil {
		t.Fatalf("failed to find non-owner key")
	}

	redis := newTestRedis()
	controller := core.NewDegradeController(redis, membership, core.DegradeThresholds{}, "region-a", false, 0)
	fallback := core.NewFallbackLimiter(owner, membership, "region-a", "region-a", core.FallbackPolicy{
		LocalCapPerWindow:      5,
		DenyWhenNotOwner:       true,
		EmergencyAllowSmallCap: true,
		EmergencyCapPerWindow:  2,
	}, controller, nil)

	decision := fallback.Allow(context.Background(), key, core.RuleParams{Window: time.Second}, 1)
	if decision.Allowed {
		t.Fatalf("expected denial when not owner")
	}
}

func TestLimiterFactory_UsesFallbackOnRedisUnhealthy(t *testing.T) {
	t.Parallel()

	redis := newTestRedis()
	redis.SetHealthy(false)
	membership := core.NewSingleInstanceMembership("self", "region")
	thresholds := core.DegradeThresholds{RedisUnhealthyFor: 10 * time.Millisecond}
	controller := core.NewDegradeController(redis, membership, thresholds, "region", false, 0)
	time.Sleep(15 * time.Millisecond)
	controller.Update(context.Background())

	policy := core.NormalizeFallbackPolicy(core.FallbackPolicy{})
	fallback := core.NewFallbackLimiter(core.NewRendezvousOwnership(membership, "region", false, true), membership, "region", "region", policy, controller, nil)
	factory := core.NewLimiterFactory(redis, fallback, controller, nil)

	limiter, _, err := factory.Create(&core.Rule{
		TenantID:  "tenant",
		Resource:  "resource",
		Algorithm: "token_bucket",
		Limit:     10,
		Window:    time.Second,
		Version:   1,
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}
	decision, err := limiter.Allow(context.Background(), []byte("key"), 1)
	if err != nil {
		t.Fatalf("unexpected allow error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected fallback allow")
	}
	if decision.Limit != policy.LocalCapPerWindow {
		t.Fatalf("expected limit %d got %d", policy.LocalCapPerWindow, decision.Limit)
	}
}
