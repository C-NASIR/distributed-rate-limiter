package ratelimit

import (
	"context"
	"testing"
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
	pool := NewLimiterPool(rules, &LimiterFactory{}, LimiterPolicy{Shards: 1, MaxEntriesShard: 2})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool)

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
	pool := NewLimiterPool(rules, &LimiterFactory{}, LimiterPolicy{Shards: 1, MaxEntriesShard: 2})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool)

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
	pool := NewLimiterPool(rules, &LimiterFactory{}, LimiterPolicy{Shards: 1, MaxEntriesShard: 2})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool)

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
