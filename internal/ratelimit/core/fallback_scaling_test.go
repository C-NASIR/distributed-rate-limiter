package core

import (
	"context"
	"testing"
	"time"
)

type alwaysOwner struct{}

func (alwaysOwner) IsOwner(ctx context.Context, key []byte) bool {
	return true
}

func TestFallbackLimiter_ScalesCapByRegionInstances(t *testing.T) {
	t.Parallel()

	instances := []InstanceInfo{
		{ID: "node-1", Region: "r1", Weight: 1},
		{ID: "node-2", Region: "r1", Weight: 1},
		{ID: "node-3", Region: "r1", Weight: 1},
		{ID: "node-4", Region: "r1", Weight: 1},
	}
	membership := NewStaticMembership("node-1", "r1", instances)
	fallback := &FallbackLimiter{
		ownership:   alwaysOwner{},
		mship:       membership,
		region:      "r1",
		regionGroup: "r1",
		policy: FallbackPolicy{
			LocalCapPerWindow:      100,
			DenyWhenNotOwner:       true,
			EmergencyAllowSmallCap: true,
			EmergencyCapPerWindow:  10,
		},
		local: &LocalLimiterStore{},
	}

	params := RuleParams{Window: time.Second}
	key := []byte("scale-key")
	for i := 0; i < 25; i++ {
		decision := fallback.Allow(context.Background(), key, params, 1)
		if !decision.Allowed {
			t.Fatalf("expected allow at %d", i)
		}
		if decision.Limit != 25 {
			t.Fatalf("expected limit 25 got %d", decision.Limit)
		}
	}
	decision := fallback.Allow(context.Background(), key, params, 1)
	if decision.Allowed {
		t.Fatalf("expected denial after scaled cap")
	}
	if decision.Limit != 25 {
		t.Fatalf("expected limit 25 got %d", decision.Limit)
	}
}
