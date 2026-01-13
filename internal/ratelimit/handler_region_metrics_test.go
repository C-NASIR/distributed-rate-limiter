package ratelimit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHandler_MetricsIncludeRegion(t *testing.T) {
	t.Parallel()

	app, err := NewApplication(&Config{Region: "r1"})
	if err != nil {
		t.Fatalf("unexpected app error: %v", err)
	}
	app.RuleCache.ReplaceAll([]*Rule{{
		TenantID:  "tenant",
		Resource:  "resource",
		Algorithm: "token_bucket",
		Limit:     10,
		Window:    time.Second,
		Version:   1,
	}})

	resp, err := app.RateLimitHandler.CheckLimit(context.Background(), &CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("unexpected check error: %v", err)
	}
	app.RateLimitHandler.ReleaseResponse(resp)

	snapshot := app.metrics.Snapshot()
	if !metricsContainRegion(snapshot, "r1") {
		t.Fatalf("expected metrics to include region")
	}
}

func metricsContainRegion(snapshot map[string]any, region string) bool {
	if snapshot == nil {
		return false
	}
	if counters, ok := snapshot["counters"].(map[string]int64); ok {
		for key := range counters {
			if strings.Contains(key, region) {
				return true
			}
		}
	}
	if latencies, ok := snapshot["latencies"].(map[string]map[string]int64); ok {
		for key := range latencies {
			if strings.Contains(key, region) {
				return true
			}
		}
	}
	return false
}
