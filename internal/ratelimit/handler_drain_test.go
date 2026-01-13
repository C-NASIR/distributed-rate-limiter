package ratelimit

import (
	"context"
	"strings"
	"testing"
)

func TestHandler_DrainingRejectsNew(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)
	tracker := NewInFlight()
	handler.inflight = tracker
	tracker.Close()

	_, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err == nil {
		t.Fatalf("expected error while draining")
	}
	if CodeOf(err) != CodeLimiterUnavailable {
		t.Fatalf("expected limiter unavailable got %q", CodeOf(err))
	}
	if strings.Contains(err.Error(), "-") {
		t.Fatalf("unexpected dash in message: %q", err.Error())
	}
}
