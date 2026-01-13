package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestDegradeController_ModeTransitions(t *testing.T) {
	t.Parallel()

	redis := NewInMemoryRedis(nil)
	membership := NewSingleInstanceMembership("self", "region")
	thresholds := DegradeThresholds{
		RedisUnhealthyFor:   20 * time.Millisecond,
		MembershipUnhealthy: 40 * time.Millisecond,
	}
	controller := NewDegradeController(redis, membership, thresholds, "region", false, 0)
	controller.Update(context.Background())
	if controller.Mode() != ModeNormal {
		t.Fatalf("expected normal mode")
	}

	redis.SetHealthy(false)
	time.Sleep(25 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != ModeDegraded {
		t.Fatalf("expected degraded mode")
	}

	membership.SetHealthy(false)
	time.Sleep(45 * time.Millisecond)
	controller.Update(context.Background())
	if controller.Mode() != ModeEmergency {
		t.Fatalf("expected emergency mode")
	}

	redis.SetHealthy(true)
	membership.SetHealthy(true)
	controller.Update(context.Background())
	if controller.Mode() != ModeNormal {
		t.Fatalf("expected normal mode after recovery")
	}
}
