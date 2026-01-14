package core_test

import (
	"context"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/core"
)

func TestDegradeController_ModeTransitions(t *testing.T) {
	t.Parallel()

	redis := newTestRedis()
	membership := core.NewSingleInstanceMembership("self", "region")
	thresholds := core.DegradeThresholds{
		RedisUnhealthyFor:   20 * time.Millisecond,
		MembershipUnhealthy: 40 * time.Millisecond,
	}
	controller := core.NewDegradeController(redis, membership, thresholds, "region", false, 0)
	controller.Update(context.Background())
	if controller.Mode() != core.ModeNormal {
		t.Fatalf("expected normal mode")
	}

	redis.SetHealthy(false)
	time.Sleep(25 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != core.ModeDegraded {
		t.Fatalf("expected degraded mode")
	}

	membership.SetHealthy(false)
	time.Sleep(45 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != core.ModeEmergency {
		t.Fatalf("expected emergency mode")
	}

	redis.SetHealthy(true)
	membership.SetHealthy(true)
	controller.Update(context.Background())
	if controller.Mode() != core.ModeNormal {
		t.Fatalf("expected normal mode after recovery")
	}
}
