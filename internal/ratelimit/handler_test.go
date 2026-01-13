package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestKeyBuilder_BuildAndReuse(t *testing.T) {
	t.Parallel()

	pool := NewByteBufferPool(32)
	keys := &KeyBuilder{bufPool: pool}

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

	pool := NewResponsePool()
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

	rules := NewRuleCache()
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{
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

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{
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

	rules := NewRuleCache()
	handler := newTestHandler(rules)

	if _, err := handler.CheckLimit(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil request")
	}
	if _, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{Resource: "resource"}); err == nil {
		t.Fatalf("expected error for empty tenant")
	}
	if _, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{TenantID: "tenant"}); err == nil {
		t.Fatalf("expected error for empty resource")
	}
}

func TestRateLimitHandler_CheckLimitBatch_Empty(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	handler := newTestHandler(rules)

	resp, err := handler.CheckLimitBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("expected empty response")
	}

	resp, err = handler.CheckLimitBatch(context.Background(), []*CheckLimitRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("expected empty response")
	}
}

func TestRateLimitHandler_CheckLimitBatch_PreservesOrder(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1},
		{TenantID: "tenant-b", Resource: "resource-2", Limit: 5, Version: 1},
	})
	handler := newTestHandler(rules)

	reqs := []*CheckLimitRequest{
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

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	reqs := []*CheckLimitRequest{
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

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	handler := newTestHandler(rules)

	reqs := []*CheckLimitRequest{
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

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	pool := NewLimiterPool(rules, &LimiterFactory{}, LimiterPolicy{Shards: 1, MaxEntriesShard: 4})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)

	reqs := []*CheckLimitRequest{
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

func releaseResponses(handler *RateLimitHandler, responses []*CheckLimitResponse) {
	for _, resp := range responses {
		handler.ReleaseResponse(resp)
	}
}

func TestRateLimitHandler_CheckLimit_EmergencyMode_UsesFallback(t *testing.T) {
	t.Parallel()

	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Limit: 10, Version: 1}})
	redis := NewInMemoryRedis(nil)
	membership := NewSingleInstanceMembership("self", "region")
	thresholds := DegradeThresholds{RedisUnhealthyFor: 10 * time.Millisecond, MembershipUnhealthy: 20 * time.Millisecond}
	degrade := NewDegradeController(redis, membership, thresholds, "region", false, 0)
	fallback := &FallbackLimiter{
		ownership:   NewRendezvousOwnership(membership, "region", false, true),
		mship:       membership,
		region:      "region",
		regionGroup: "region",
		policy:      normalizeFallbackPolicy(FallbackPolicy{}),
		mode:        degrade,
		local:       &LocalLimiterStore{},
	}
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: degrade}
	pool := NewLimiterPool(rules, factory, LimiterPolicy{Shards: 1, MaxEntriesShard: 2})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)

	redis.SetHealthy(false)
	membership.SetHealthy(false)
	time.Sleep(25 * time.Millisecond)
	degrade.Update(context.Background())

	resp, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{
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

func newTestHandler(rules *RuleCache) *RateLimitHandler {
	redis := NewInMemoryRedis(nil)
	membership := NewSingleInstanceMembership("self", "region")
	degrade := NewDegradeController(redis, membership, DegradeThresholds{}, "region", false, 0)
	fallback := &FallbackLimiter{
		ownership:   NewRendezvousOwnership(membership, "region", false, true),
		mship:       membership,
		region:      "region",
		regionGroup: "region",
		policy:      normalizeFallbackPolicy(FallbackPolicy{}),
		mode:        degrade,
		local:       &LocalLimiterStore{},
	}
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: degrade}
	pool := NewLimiterPool(rules, factory, LimiterPolicy{Shards: 1, MaxEntriesShard: 4})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	return NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, nil)
}
