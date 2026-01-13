package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestDegradeController_QuorumAffectsMode(t *testing.T) {
	t.Parallel()

	instances := []InstanceInfo{
		{ID: "node-1", Region: "r1", Weight: 1},
		{ID: "node-2", Region: "r2", Weight: 1},
		{ID: "node-3", Region: "r2", Weight: 1},
		{ID: "node-4", Region: "r3", Weight: 1},
	}
	membership := NewStaticMembership("node-1", "r1", instances)
	redis := NewInMemoryRedis(nil)
	redis.SetHealthy(false)
	thresholds := DegradeThresholds{
		RedisUnhealthyFor:   10 * time.Millisecond,
		MembershipUnhealthy: 10 * time.Millisecond,
	}
	controller := NewDegradeController(redis, membership, thresholds, "r1", true, 0.5)

	time.Sleep(15 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != ModeEmergency {
		t.Fatalf("expected emergency mode when quorum missing")
	}
}
