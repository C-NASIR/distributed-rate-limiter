package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestChaosRunnerSubset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	harness, err := NewChaosHarness()
	if err != nil {
		t.Fatalf("failed to build harness: %v", err)
	}

	scenarios := []ChaosScenario{
		scenarioRedisRecover(),
		scenarioPubSubRepair(),
	}

	if err := runChaosScenarios(ctx, harness, scenarios); err != nil {
		t.Fatalf("chaos scenarios failed: %v", err)
	}
}
