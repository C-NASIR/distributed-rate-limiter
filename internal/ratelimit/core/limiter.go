// Package core provides limiter primitives.
package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Limiter decides whether requests are allowed.
type Limiter interface {
	Allow(ctx context.Context, key []byte, cost int64) (*Decision, error)
	AllowBatch(ctx context.Context, keys [][]byte, costs []int64) ([]*Decision, error)
	RuleVersion() int64
	Close() error
}

// RuleParams captures limiter parameters.
type RuleParams struct {
	Limit   int64
	Window  time.Duration
	Burst   int64
	Version int64
}

// LimiterFactory constructs limiters from rules.
type LimiterFactory struct {
	redis    RedisClient
	fallback *FallbackLimiter
	mode     *DegradeController
	breaker  *CircuitBreaker
	mu       sync.Mutex
	coalesceEnabled bool
}

// NewLimiterFactory constructs a LimiterFactory.
func NewLimiterFactory(redis RedisClient, fallback *FallbackLimiter, mode *DegradeController, breaker *CircuitBreaker) *LimiterFactory {
	return &LimiterFactory{
		redis:    redis,
		fallback: fallback,
		mode:     mode,
		breaker:  breaker,
	}
}

// Create builds a limiter from a rule.
func (factory *LimiterFactory) Create(rule *Rule) (Limiter, RuleParams, error) {
	if factory == nil {
		return nil, RuleParams{}, errors.New("limiter factory is nil")
	}
	if factory.redis == nil || factory.fallback == nil || factory.mode == nil {
		return nil, RuleParams{}, errors.New("limiter factory is not configured")
	}
	if rule == nil {
		return nil, RuleParams{}, errors.New("rule is required")
	}
	if rule.TenantID == "" || rule.Resource == "" {
		return nil, RuleParams{}, errors.New("rule must include tenant and resource")
	}
	if rule.Limit <= 0 {
		return nil, RuleParams{}, errors.New("rule limit must be positive")
	}
	params := RuleParams{
		Limit:   rule.Limit,
		Window:  rule.Window,
		Burst:   rule.BurstSize,
		Version: rule.Version,
	}
	breaker := factory.breaker
	if breaker == nil {
		factory.mu.Lock()
		if factory.breaker == nil {
			factory.breaker = NewCircuitBreaker(CircuitOptions{})
		}
		breaker = factory.breaker
		factory.mu.Unlock()
	}
	algo, err := normalizeAlgorithm(rule.Algorithm)
	if err != nil {
		return nil, RuleParams{}, err
	}
	return &redisLimiter{
		algo:     algo,
		redis:    factory.redis,
		fallback: factory.fallback,
		mode:     factory.mode,
		breaker:  breaker,
		params:   params,
	}, params, nil
}

type limiterAlgorithm string

const (
	limiterTokenBucket   limiterAlgorithm = "token_bucket"
	limiterFixedWindow   limiterAlgorithm = "fixed_window"
	limiterSlidingWindow limiterAlgorithm = "sliding_window"
)

type redisLimiter struct {
	algo     limiterAlgorithm
	redis    RedisClient
	fallback *FallbackLimiter
	mode     *DegradeController
	breaker  *CircuitBreaker
	params   RuleParams
}

func (lim *redisLimiter) Allow(ctx context.Context, key []byte, cost int64) (*Decision, error) {
	if lim == nil {
		return nil, errors.New("limiter is nil")
	}
	if lim.mode != nil && lim.mode.Mode() == ModeEmergency {
		return lim.fallbackDecision(ctx, key, cost)
	}
	if lim.breaker != nil && !lim.breaker.Allow() {
		return lim.fallbackDecision(ctx, key, cost)
	}
	decision, err := lim.exec(ctx, key, cost)
	if err != nil {
		if lim.breaker != nil {
			lim.breaker.OnFailure()
		}
		return lim.fallbackDecision(ctx, key, cost)
	}
	if lim.breaker != nil {
		lim.breaker.OnSuccess()
	}
	return decision, nil
}

func (lim *redisLimiter) AllowBatch(ctx context.Context, keys [][]byte, costs []int64) ([]*Decision, error) {
	if lim == nil {
		return nil, errors.New("limiter is nil")
	}
	if lim.mode != nil && lim.mode.Mode() == ModeEmergency {
		return lim.fallbackDecisions(ctx, keys, costs)
	}
	if lim.breaker != nil && !lim.breaker.Allow() {
		return lim.fallbackDecisions(ctx, keys, costs)
	}
	if lim.redis == nil {
		return lim.fallbackDecisions(ctx, keys, costs)
	}
	pipe := lim.redis.Pipeline()
	for i, key := range keys {
		cost := int64(0)
		if i < len(costs) {
			cost = costs[i]
		}
		lim.queue(pipe, string(key), cost)
	}
	decisions, err := pipe.Exec(ctx)
	if err != nil || len(decisions) != len(keys) {
		if lim.breaker != nil {
			lim.breaker.OnFailure()
		}
		return lim.fallbackDecisions(ctx, keys, costs)
	}
	if lim.breaker != nil {
		lim.breaker.OnSuccess()
	}
	return decisions, nil
}

func (lim *redisLimiter) RuleVersion() int64 {
	return lim.params.Version
}

func (lim *redisLimiter) Close() error {
	return nil
}

func (lim *redisLimiter) exec(ctx context.Context, key []byte, cost int64) (*Decision, error) {
	if lim.redis == nil {
		return nil, errors.New("redis is nil")
	}
	keyStr := string(key)
	switch lim.algo {
	case limiterTokenBucket:
		return lim.redis.ExecTokenBucket(ctx, keyStr, lim.params, cost)
	case limiterFixedWindow:
		return lim.redis.ExecFixedWindow(ctx, keyStr, lim.params, cost)
	case limiterSlidingWindow:
		return lim.redis.ExecSlidingWindow(ctx, keyStr, lim.params, cost)
	default:
		return nil, errors.New("unsupported limiter algorithm")
	}
}

func (lim *redisLimiter) queue(pipe RedisPipeline, key string, cost int64) {
	switch lim.algo {
	case limiterTokenBucket:
		pipe.ExecTokenBucket(key, lim.params, cost)
	case limiterFixedWindow:
		pipe.ExecFixedWindow(key, lim.params, cost)
	case limiterSlidingWindow:
		pipe.ExecSlidingWindow(key, lim.params, cost)
	}
}

func (lim *redisLimiter) fallbackDecisions(ctx context.Context, keys [][]byte, costs []int64) ([]*Decision, error) {
	if lim.fallback == nil {
		return nil, errors.New("fallback is nil")
	}
	decisions := make([]*Decision, len(keys))
	for i, key := range keys {
		cost := int64(0)
		if i < len(costs) {
			cost = costs[i]
		}
		decisions[i] = lim.fallback.Allow(ctx, key, lim.params, cost)
	}
	return decisions, errUsedFallback{reason: "fallback"}
}

func (lim *redisLimiter) fallbackDecision(ctx context.Context, key []byte, cost int64) (*Decision, error) {
	if lim.fallback == nil {
		return nil, errors.New("fallback is nil")
	}
	return lim.fallback.Allow(ctx, key, lim.params, cost), errUsedFallback{reason: "fallback"}
}

type errUsedFallback struct {
	reason string
}

func (e errUsedFallback) Error() string {
	if e.reason == "" {
		return "fallback"
	}
	return e.reason
}

func normalizeAlgorithm(algo string) (limiterAlgorithm, error) {
	if algo == "" {
		return "", errors.New("algorithm is required")
	}
	normalized := strings.ToLower(strings.TrimSpace(algo))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "token_bucket", "tokenbucket":
		return limiterTokenBucket, nil
	case "fixed_window", "fixedwindow":
		return limiterFixedWindow, nil
	case "sliding_window", "slidingwindow":
		return limiterSlidingWindow, nil
	default:
		return "", errors.New("unsupported algorithm")
	}
}
