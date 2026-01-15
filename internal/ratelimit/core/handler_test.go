package core_test

import (
	"context"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/core"
)

func TestKeyBuilder_BuildAndReuse(t *testing.T) {
	t.Parallel()

	pool := core.NewByteBufferPool(32)
	keys := core.NewKeyBuilder(pool)

	key1 := keys.BuildKey("tenant", "user", "resource")
	if got := string(key1); got != "tenant\x1fuser\x1fresource" {
		t.Fatalf("unexpected key: %s", got)
	}
	keys.ReleaseKey(key1)

	key2 := keys.BuildKey("tenant", "user", "resource")
	if got := string(key2); got != "tenant\x1fuser\x1fresource" {
		t.Fatalf("unexpected key: %s", got)
	}
	keys.ReleaseKey(key2)

	key3 := keys.BuildKey("tenant", "user", "resource")
	if got := string(key3); got != "tenant\x1fuser\x1fresource" {
		t.Fatalf("unexpected key: %s", got)
	}
	keys.ReleaseKey(key3)
}

func TestResponsePool_ResetsFields(t *testing.T) {
	t.Parallel()

	pool := core.NewResponsePool()
	resp := pool.Get()
	resp.Allowed = true
	resp.Remaining = 10
	resp.Limit = 20
	resp.ErrorCode = "ERR"
	pool.Put(resp)

	resp2 := pool.Get()
	if resp2.Allowed || resp2.Remaining != 0 || resp2.Limit != 0 || resp2.ErrorCode != "" {
		t.Fatalf("response not reset: %#v", resp2)
	}
	pool.Put(resp2)
}

func TestRateLimitHandler_RuleNotFound(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Allowed || resp.ErrorCode != "RULE_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	handler.ReleaseResponse(resp)
}

func TestRateLimitHandler_Success(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Allowed || resp.ErrorCode != "" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	handler.ReleaseResponse(resp)
}

func TestRateLimitHandler_InvalidRequest_ReturnsError(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	handler := newTestHandler(rules)

	if _, err := handler.CheckLimit(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil request")
	}
	if _, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{Resource: "resource"}); err == nil {
		t.Fatalf("expected error for empty tenant")
	}
	if _, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{TenantID: "tenant"}); err == nil {
		t.Fatalf("expected error for empty resource")
	}
}

func TestRateLimitHandler_CheckLimitBatch_Empty(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimitBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("expected empty response")
	}

	resp, err = handler.CheckLimitBatch(context.Background(), []*core.CheckLimitRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("expected empty response")
	}
}

func TestRateLimitHandler_CheckLimitBatch_PreservesOrder(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Algorithm: "token_bucket", Limit: 10, Version: 1},
		{TenantID: "tenant-b", Resource: "resource-2", Algorithm: "token_bucket", Limit: 5, Version: 1},
	})
	handler := newTestHandler(rules)

	reqs := []*core.CheckLimitRequest{
		{TenantID: "tenant-a", Resource: "resource-1", UserID: "user-1", Cost: 1},
		{TenantID: "tenant-b", Resource: "resource-2", UserID: "user-2", Cost: 1},
		{TenantID: "tenant-a", Resource: "resource-1", UserID: "user-3", Cost: 2},
		{TenantID: "tenant-b", Resource: "resource-2", UserID: "user-4", Cost: 1},
	}

	resp, err := handler.CheckLimitBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != len(reqs) {
		t.Fatalf("expected %d responses, got %d", len(reqs), len(resp))
	}
	for i, response := range resp {
		if response == nil {
			t.Fatalf("response %d is nil", i)
		}
		if !response.Allowed || response.ErrorCode != "" {
			t.Fatalf("unexpected response at %d: %#v", i, response)
		}
	}
	releaseResponses(handler, resp)
}

func TestRateLimitHandler_CheckLimitBatch_PerItemValidation(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	reqs := []*core.CheckLimitRequest{
		nil,
		{TenantID: "", Resource: "resource", UserID: "user", Cost: 1},
		{TenantID: "tenant", Resource: "resource", UserID: "user", Cost: 0},
		{TenantID: "tenant", Resource: "resource", UserID: "user", Cost: 1},
	}

	resp, err := handler.CheckLimitBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0].ErrorCode != "INVALID_REQUEST" || resp[0].Allowed {
		t.Fatalf("unexpected response for nil request: %#v", resp[0])
	}
	if resp[1].ErrorCode != "INVALID_REQUEST" || resp[1].Allowed {
		t.Fatalf("unexpected response for empty tenant: %#v", resp[1])
	}
	if resp[2].ErrorCode != "INVALID_COST" || resp[2].Allowed {
		t.Fatalf("unexpected response for invalid cost: %#v", resp[2])
	}
	if !resp[3].Allowed || resp[3].ErrorCode != "" {
		t.Fatalf("unexpected response for valid request: %#v", resp[3])
	}
	releaseResponses(handler, resp)
}

func TestRateLimitHandler_CheckLimitBatch_RuleNotFoundPerItem(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	reqs := []*core.CheckLimitRequest{
		{TenantID: "tenant", Resource: "missing", UserID: "user", Cost: 1},
		{TenantID: "tenant", Resource: "resource", UserID: "user", Cost: 1},
	}

	resp, err := handler.CheckLimitBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp[0].ErrorCode != "RULE_NOT_FOUND" || resp[0].Allowed {
		t.Fatalf("unexpected response for missing rule: %#v", resp[0])
	}
	if !resp[1].Allowed || resp[1].ErrorCode != "" {
		t.Fatalf("unexpected response for valid rule: %#v", resp[1])
	}
	releaseResponses(handler, resp)
}

func TestRateLimitHandler_CheckLimitBatch_LimiterUnavailablePerGroup(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Version: 1}})
	pool := core.NewLimiterPool(rules, &core.LimiterFactory{}, core.LimiterPolicy{Shards: 1, MaxEntriesShard: 4})
	keys := core.NewKeyBuilder(core.NewByteBufferPool(64))
	respPool := core.NewResponsePool()
	handler := core.NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)

	reqs := []*core.CheckLimitRequest{
		{TenantID: "tenant", Resource: "resource", UserID: "user-1", Cost: 1},
		{TenantID: "tenant", Resource: "resource", UserID: "user-2", Cost: 1},
	}

	resp, err := handler.CheckLimitBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, response := range resp {
		if response.ErrorCode != "LIMITER_UNAVAILABLE" || response.Allowed {
			t.Fatalf("unexpected response at %d: %#v", i, response)
		}
	}
	releaseResponses(handler, resp)
}

func releaseResponses(handler *core.RateLimitHandler, responses []*core.CheckLimitResponse) {
	for _, resp := range responses {
		handler.ReleaseResponse(resp)
	}
}

func TestRateLimitHandler_CheckLimit_EmergencyMode_UsesFallback(t *testing.T) {
	t.Parallel()

	rules := core.NewRuleCache()
	rules.ReplaceAll([]*core.Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Version: 1}})
	redis := newTestRedis()
	membership := core.NewSingleInstanceMembership("self", "region")
	thresholds := core.DegradeThresholds{RedisUnhealthyFor: 10 * time.Millisecond, MembershipUnhealthy: 20 * time.Millisecond}
	degrade := core.NewDegradeController(redis, membership, thresholds, "region", false, 0)
	fallback := core.NewFallbackLimiter(core.NewRendezvousOwnership(membership, "region", false, true), membership, "region", "region", core.NormalizeFallbackPolicy(core.FallbackPolicy{}), degrade, nil)
	factory := core.NewLimiterFactory(redis, fallback, degrade, nil)
	pool := core.NewLimiterPool(rules, factory, core.LimiterPolicy{Shards: 1, MaxEntriesShard: 2})
	keys := core.NewKeyBuilder(core.NewByteBufferPool(64))
	respPool := core.NewResponsePool()
	handler := core.NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)

	redis.SetHealthy(false)
	membership.SetHealthy(false)
	time.Sleep(25 * time.Millisecond)
	degrade.Update(context.Background())

	resp, err := handler.CheckLimit(context.Background(), &core.CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ErrorCode != "" {
		t.Fatalf("unexpected error code: %#v", resp)
	}
	handler.ReleaseResponse(resp)
}

func newTestHandler(rules *core.RuleCache) *core.RateLimitHandler {
	redis := newTestRedis()
	membership := core.NewSingleInstanceMembership("self", "region")
	degrade := core.NewDegradeController(redis, membership, core.DegradeThresholds{}, "region", false, 0)
	fallback := core.NewFallbackLimiter(core.NewRendezvousOwnership(membership, "region", false, true), membership, "region", "region", core.NormalizeFallbackPolicy(core.FallbackPolicy{}), degrade, nil)
	factory := core.NewLimiterFactory(redis, fallback, degrade, nil)
	pool := core.NewLimiterPool(rules, factory, core.LimiterPolicy{Shards: 1, MaxEntriesShard: 4})
	keys := core.NewKeyBuilder(core.NewByteBufferPool(64))
	respPool := core.NewResponsePool()
	return core.NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)
}
