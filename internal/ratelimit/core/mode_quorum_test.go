package core_test

import (
	"context"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/core"
)

func TestDegradeController_QuorumAffectsMode(t *testing.T) {
	t.Parallel()

	instances := []core.InstanceInfo{
		{ID: "node-1", Region: "r1", Weight: 1},
		{ID: "node-2", Region: "r2", Weight: 1},
		{ID: "node-3", Region: "r2", Weight: 1},
		{ID: "node-4", Region: "r3", Weight: 1},
	}
	membership := core.NewStaticMembership("node-1", "r1", instances)
	redis := newTestRedis()
	redis.SetHealthy(false)
	thresholds := core.DegradeThresholds{
		RedisUnhealthyFor:   10 * time.Millisecond,
		MembershipUnhealthy: 10 * time.Millisecond,
	}
	controller := core.NewDegradeController(redis, membership, thresholds, "r1", true, 0.5)

	time.Sleep(15 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != core.ModeEmergency {
		t.Fatalf("expected emergency mode when quorum missing")
	}
}
