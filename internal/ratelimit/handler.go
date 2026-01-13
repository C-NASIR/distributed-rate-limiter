// Package ratelimit provides the rate limit handler.
package ratelimit

import (
	"context"
	"errors"
)

// RateLimitHandler handles rate limit requests.
type RateLimitHandler struct {
	rules    *RuleCache
	pool     *LimiterPool
	keys     *KeyBuilder
	region   string
	respPool *ResponsePool
}

// NewRateLimitHandler constructs a RateLimitHandler.
func NewRateLimitHandler(rules *RuleCache, pool *LimiterPool, keys *KeyBuilder, region string, rp *ResponsePool) *RateLimitHandler {
	return &RateLimitHandler{
		rules:    rules,
		pool:     pool,
		keys:     keys,
		region:   region,
		respPool: rp,
	}
}

// CheckLimit evaluates a rate limit request.
func (h *RateLimitHandler) CheckLimit(ctx context.Context, req *CheckLimitRequest) (*CheckLimitResponse, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}
	if req.TenantID == "" {
		return nil, errors.New("tenant id is required")
	}
	if req.Resource == "" {
		return nil, errors.New("resource is required")
	}
	if h == nil || h.rules == nil || h.pool == nil || h.keys == nil || h.respPool == nil {
		return nil, errors.New("handler is not initialized")
	}
	if _, ok := h.rules.Get(req.TenantID, req.Resource); !ok {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "RULE_NOT_FOUND"
		return resp, nil
	}
	handle, _, err := h.pool.Acquire(ctx, req.TenantID, req.Resource)
	if err != nil {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "LIMITER_UNAVAILABLE"
		return resp, nil
	}
	defer handle.Release()

	key := h.keys.BuildKey(req.TenantID, req.UserID, req.Resource)
	defer h.keys.ReleaseKey(key)

	decision, err := handle.Limiter().Allow(ctx, key, req.Cost)
	if err != nil || decision == nil {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "LIMITER_ERROR"
		return resp, nil
	}

	resp := h.respPool.Get()
	resp.Allowed = decision.Allowed
	resp.Remaining = decision.Remaining
	resp.Limit = decision.Limit
	resp.ResetAfter = decision.ResetAfter
	resp.RetryAfter = decision.RetryAfter
	resp.ErrorCode = ""
	return resp, nil
}

// CheckLimitBatch evaluates rate limit requests in batch.
func (h *RateLimitHandler) CheckLimitBatch(ctx context.Context, reqs []*CheckLimitRequest) ([]*CheckLimitResponse, error) {
	return nil, errors.New("not implemented")
}

// ReleaseResponse releases a pooled response.
func (h *RateLimitHandler) ReleaseResponse(resp *CheckLimitResponse) {
	if h == nil || h.respPool == nil || resp == nil {
		return
	}
	h.respPool.Put(resp)
}
