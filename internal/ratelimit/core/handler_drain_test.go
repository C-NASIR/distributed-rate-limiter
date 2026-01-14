package core_test

import (
	"context"
	"strings"
	"testing"

	"ratelimit/internal/ratelimit/core"
)

func TestHandler_DrainingRejectsNew(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)
	tracker := core.NewInFlight()
	handler.SetInFlight(tracker)
	tracker.Close()

	_, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err == nil {
		t.Fatalf("expected error while draining")
	}
	if core.CodeOf(err) != core.CodeLimiterUnavailable {
		t.Fatalf("expected limiter unavailable got %q", core.CodeOf(err))
	}
	if strings.Contains(err.Error(), "-") {
		t.Fatalf("unexpected dash in message: %q", err.Error())
	}
}
