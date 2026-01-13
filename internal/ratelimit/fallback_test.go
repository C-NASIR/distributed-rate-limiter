package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRendezvousOwnership_StableOwner(t *testing.T) {
	t.Parallel()

	membership := NewStaticMembership("node-a", []string{"node-a", "node-b", "node-c"})
	owner := &RendezvousOwnership{m: membership}
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

	membership := NewStaticMembership("node-a", []string{"node-a", "node-b"})
	owner := &RendezvousOwnership{m: membership}
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

	redis := NewInMemoryRedis(nil)
	controller := NewDegradeController(redis, membership, DegradeThresholds{})
	fallback := &FallbackLimiter{
		ownership: owner,
		policy: FallbackPolicy{
			LocalCapPerWindow:      5,
			DenyWhenNotOwner:       true,
			EmergencyAllowSmallCap: true,
			EmergencyCapPerWindow:  2,
		},
		mode:  controller,
		local: &LocalLimiterStore{},
	}

	decision := fallback.Allow(context.Background(), key, RuleParams{Window: time.Second}, 1)
	if decision.Allowed {
		t.Fatalf("expected denial when not owner")
	}
}

func TestLimiterFactory_UsesFallbackOnRedisUnhealthy(t *testing.T) {
	t.Parallel()

	redis := NewInMemoryRedis(nil)
	redis.SetHealthy(false)
	membership := NewStaticMembership("self", []string{"self"})
	thresholds := DegradeThresholds{RedisUnhealthyFor: 10 * time.Millisecond}
	controller := NewDegradeController(redis, membership, thresholds)
	time.Sleep(15 * time.Millisecond)
	controller.Update(context.Background())

	policy := normalizeFallbackPolicy(FallbackPolicy{})
	fallback := &FallbackLimiter{
		ownership: &RendezvousOwnership{m: membership},
		policy:    policy,
		mode:      controller,
		local:     &LocalLimiterStore{},
	}
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: controller}

	limiter, _, err := factory.Create(&Rule{
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
