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
	batch    *BatchPlanner
}

// NewRateLimitHandler constructs a RateLimitHandler.
func NewRateLimitHandler(rules *RuleCache, pool *LimiterPool, keys *KeyBuilder, region string, rp *ResponsePool) *RateLimitHandler {
	return &RateLimitHandler{
		rules:    rules,
		pool:     pool,
		keys:     keys,
		region:   region,
		respPool: rp,
		batch:    newBatchPlanner(),
	}
}

// CheckLimit evaluates a rate limit request.
func (h *RateLimitHandler) CheckLimit(ctx context.Context, req *CheckLimitRequest) (*CheckLimitResponse, error) {
	if req == nil {
		return nil, ErrInvalidInput
	}
	if req.TenantID == "" {
		return nil, ErrInvalidInput
	}
	if req.Resource == "" {
		return nil, ErrInvalidInput
	}
	if req.UserID == "" {
		return nil, ErrInvalidInput
	}
	if req.Cost <= 0 {
		return nil, ErrInvalidInput
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
	if reqs == nil || len(reqs) == 0 {
		return []*CheckLimitResponse{}, nil
	}
	if h == nil || h.rules == nil || h.pool == nil || h.keys == nil || h.respPool == nil {
		return nil, errors.New("handler is not initialized")
	}

	responses := make([]*CheckLimitResponse, len(reqs))
	validReqs := make([]*CheckLimitRequest, len(reqs))
	for i, req := range reqs {
		resp := h.respPool.Get()
		responses[i] = resp
		if req == nil {
			resp.Allowed = false
			resp.ErrorCode = "INVALID_REQUEST"
			continue
		}
		if req.TenantID == "" || req.Resource == "" || req.UserID == "" {
			resp.Allowed = false
			resp.ErrorCode = "INVALID_REQUEST"
			continue
		}
		if req.Cost <= 0 {
			resp.Allowed = false
			resp.ErrorCode = "INVALID_COST"
			continue
		}
		validReqs[i] = req
	}

	planner := h.batch
	if planner == nil {
		planner = newBatchPlanner()
		h.batch = planner
	}
	plan := planner.Plan(validReqs)
	defer planner.Release(plan)

	for _, group := range plan.Groups {
		if _, ok := h.rules.Get(group.TenantID, group.Resource); !ok {
			for _, idx := range group.Indexes {
				resp := responses[idx]
				resp.Allowed = false
				resp.ErrorCode = "RULE_NOT_FOUND"
			}
			continue
		}
		handle, _, err := h.pool.Acquire(ctx, group.TenantID, group.Resource)
		if err != nil {
			for _, idx := range group.Indexes {
				resp := responses[idx]
				resp.Allowed = false
				resp.ErrorCode = "LIMITER_UNAVAILABLE"
			}
			continue
		}
		limiter := handle.Limiter()
		for i, idx := range group.Indexes {
			req := validReqs[idx]
			if req == nil {
				continue
			}
			group.Keys[i] = h.keys.BuildKey(req.TenantID, req.UserID, req.Resource)
		}

		decisions, err := limiter.AllowBatch(ctx, group.Keys, group.Costs)
		if err != nil || len(decisions) != len(group.Indexes) {
			for _, idx := range group.Indexes {
				resp := responses[idx]
				resp.Allowed = false
				resp.ErrorCode = "LIMITER_ERROR"
			}
		} else {
			for i, idx := range group.Indexes {
				decision := decisions[i]
				resp := responses[idx]
				if decision == nil {
					resp.Allowed = false
					resp.ErrorCode = "LIMITER_ERROR"
					continue
				}
				resp.Allowed = decision.Allowed
				resp.Remaining = decision.Remaining
				resp.Limit = decision.Limit
				resp.ResetAfter = decision.ResetAfter
				resp.RetryAfter = decision.RetryAfter
				resp.ErrorCode = ""
			}
		}

		for _, key := range group.Keys {
			if key != nil {
				h.keys.ReleaseKey(key)
			}
		}
		handle.Release()
	}

	return responses, nil
}

// ReleaseResponse releases a pooled response.
func (h *RateLimitHandler) ReleaseResponse(resp *CheckLimitResponse) {
	if h == nil || h.respPool == nil || resp == nil {
		return
	}
	h.respPool.Put(resp)
}

func newBatchPlanner() *BatchPlanner {
	return &BatchPlanner{
		indexPool: NewIndexPool(),
		keyPool:   NewKeyPool(),
		costPool:  NewCostPool(),
	}
}
