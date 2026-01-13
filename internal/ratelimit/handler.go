// Package ratelimit provides the rate limit handler.
package ratelimit

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// RateLimitHandler handles rate limit requests.
type RateLimitHandler struct {
	rules    *RuleCache
	pool     *LimiterPool
	keys     *KeyBuilder
	region   string
	respPool *ResponsePool
	batch    *BatchPlanner
	tracer   Tracer
	sampler  Sampler
	metrics  Metrics
	coalescer *Coalescer
}

// NewRateLimitHandler constructs a RateLimitHandler.
func NewRateLimitHandler(rules *RuleCache, pool *LimiterPool, keys *KeyBuilder, region string, rp *ResponsePool, tracer Tracer, sampler Sampler, metrics Metrics, coalescer *Coalescer) *RateLimitHandler {
	if tracer == nil {
		tracer = NoopTracer{}
	}
	if sampler == nil {
		sampler = HashSampler{rate: 100}
	}
	if metrics == nil {
		metrics = NewInMemoryMetrics()
	}
	if coalescer == nil {
		coalescer = NewCoalescer(0, 0)
	}
	return &RateLimitHandler{
		rules:     rules,
		pool:      pool,
		keys:      keys,
		region:    region,
		respPool:  rp,
		batch:     newBatchPlanner(),
		tracer:    tracer,
		sampler:   sampler,
		metrics:   metrics,
		coalescer: coalescer,
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
	start := time.Now()
	span := Span(nil)
	if h.sampler != nil && h.sampler.Sampled(req.TraceID) {
		ctx, span = h.tracer.StartSpan(ctx, "check")
		span.SetAttribute("tenant_id", req.TenantID)
		span.SetAttribute("resource", req.Resource)
		span.SetAttribute("region", h.region)
	}
	defer func() {
		if span != nil {
			span.End()
		}
		if h.metrics != nil {
			h.metrics.ObserveLatency("check", time.Since(start), h.region)
		}
	}()
	rule, ok := h.rules.Get(req.TenantID, req.Resource)
	algorithm := ""
	if ok && rule != nil {
		algorithm = rule.Algorithm
	}
	if !ok {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "RULE_NOT_FOUND"
		h.recordCheckMetrics(resp, algorithm, false)
		return resp, nil
	}
	handle, _, err := h.pool.Acquire(ctx, req.TenantID, req.Resource)
	if err != nil {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "LIMITER_UNAVAILABLE"
		h.recordCheckMetrics(resp, algorithm, false)
		return resp, nil
	}
	defer handle.Release()

	key := h.keys.BuildKey(req.TenantID, req.UserID, req.Resource)
	defer h.keys.ReleaseKey(key)
	coalesceKey := h.keys.KeyToString(key) + "\x1f" + strconv.FormatInt(req.Cost, 10)
	decision, err := h.coalescer.Do(ctx, coalesceKey, func() (*Decision, error) {
		return handle.Limiter().Allow(ctx, key, req.Cost)
	})
	usedFallback := false
	if err != nil {
		var fallback errUsedFallback
		if errors.As(err, &fallback) {
			usedFallback = true
			err = nil
		}
	}
	if err != nil || decision == nil {
		resp := h.respPool.Get()
		resp.Allowed = false
		resp.ErrorCode = "LIMITER_ERROR"
		if span != nil {
			span.RecordError(err)
		}
		h.recordCheckMetrics(resp, algorithm, usedFallback)
		return resp, nil
	}

	resp := h.respPool.Get()
	resp.Allowed = decision.Allowed
	resp.Remaining = decision.Remaining
	resp.Limit = decision.Limit
	resp.ResetAfter = decision.ResetAfter
	resp.RetryAfter = decision.RetryAfter
	resp.ErrorCode = ""
	h.recordCheckMetrics(resp, algorithm, usedFallback)
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
	start := time.Now()
	defer func() {
		if h.metrics != nil {
			h.metrics.ObserveLatency("checkBatch", time.Since(start), h.region)
		}
	}()

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

	sampled := false
	if h.sampler != nil {
		for _, req := range reqs {
			if req != nil && h.sampler.Sampled(req.TraceID) {
				sampled = true
				break
			}
		}
	}

	for _, group := range plan.Groups {
		span := Span(nil)
		groupCtx := ctx
		if sampled {
			groupCtx, span = h.tracer.StartSpan(groupCtx, "checkBatchGroup")
			span.SetAttribute("tenant_id", group.TenantID)
			span.SetAttribute("resource", group.Resource)
			span.SetAttribute("region", h.region)
		}
		if _, ok := h.rules.Get(group.TenantID, group.Resource); !ok {
			for _, idx := range group.Indexes {
				resp := responses[idx]
				resp.Allowed = false
				resp.ErrorCode = "RULE_NOT_FOUND"
			}
			if span != nil {
				span.End()
			}
			continue
		}
		handle, _, err := h.pool.Acquire(groupCtx, group.TenantID, group.Resource)
		if err != nil {
			for _, idx := range group.Indexes {
				resp := responses[idx]
				resp.Allowed = false
				resp.ErrorCode = "LIMITER_UNAVAILABLE"
			}
			if span != nil {
				span.End()
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

		decisions, err := limiter.AllowBatch(groupCtx, group.Keys, group.Costs)
		usedFallback := false
		if err != nil {
			var fallback errUsedFallback
			if errors.As(err, &fallback) {
				usedFallback = true
				err = nil
			}
		}
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
				if usedFallback && h.metrics != nil {
					h.metrics.IncFallback("fallback", h.region)
				}
			}
		}

		for _, key := range group.Keys {
			if key != nil {
				h.keys.ReleaseKey(key)
			}
		}
		handle.Release()
		if span != nil {
			span.End()
		}
	}

	for _, resp := range responses {
		h.recordBatchItemError(resp)
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

func (h *RateLimitHandler) recordCheckMetrics(resp *CheckLimitResponse, algorithm string, usedFallback bool) {
	if h.metrics == nil || resp == nil {
		return
	}
	result := resp.ErrorCode
	if result == "" {
		if resp.Allowed {
			result = "allowed"
		} else {
			result = "denied"
		}
	}
	h.metrics.IncCheck(result, algorithm, h.region)
	if resp.ErrorCode == "LIMITER_ERROR" || resp.ErrorCode == "LIMITER_UNAVAILABLE" {
		h.metrics.IncRedisError("allow", h.region)
	}
	if usedFallback {
		h.metrics.IncFallback("fallback", h.region)
	}
}

func (h *RateLimitHandler) recordBatchItemError(resp *CheckLimitResponse) {
	if h.metrics == nil || resp == nil || resp.ErrorCode == "" {
		return
	}
	h.metrics.IncBatchItemError(resp.ErrorCode, h.region)
}
