// Package ratelimit provides an in-memory Redis implementation.
package ratelimit

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// RedisClient provides limiter operations.
type RedisClient interface {
	Healthy(ctx context.Context) bool
	Pipeline() RedisPipeline
	ExecTokenBucket(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error)
	ExecFixedWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error)
	ExecSlidingWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error)
}

// RedisPipeline queues redis operations.
type RedisPipeline interface {
	ExecTokenBucket(key string, params RuleParams, cost int64)
	ExecFixedWindow(key string, params RuleParams, cost int64)
	ExecSlidingWindow(key string, params RuleParams, cost int64)
	Exec(ctx context.Context) ([]*Decision, error)
}

// InMemoryRedis implements RedisClient in memory.
type InMemoryRedis struct {
	mu             sync.Mutex
	now            func() time.Time
	tokenBuckets   map[string]*tbState
	fixedWindows   map[string]*fwState
	slidingWindows map[string]*swState
	healthy        atomic.Bool
	windowDefault  time.Duration
}

type tbState struct {
	tokens float64
	last   time.Time
}

type fwState struct {
	windowStart time.Time
	used        int64
}

type swState struct {
	window time.Duration
	events []swEvent
}

type swEvent struct {
	t    time.Time
	cost int64
}

// NewInMemoryRedis constructs an in-memory redis.
func NewInMemoryRedis(now func() time.Time) *InMemoryRedis {
	if now == nil {
		now = time.Now
	}
	redis := &InMemoryRedis{
		now:            now,
		tokenBuckets:   make(map[string]*tbState),
		fixedWindows:   make(map[string]*fwState),
		slidingWindows: make(map[string]*swState),
		windowDefault:  time.Second,
	}
	redis.healthy.Store(true)
	return redis
}

// Healthy reports redis health.
func (r *InMemoryRedis) Healthy(ctx context.Context) bool {
	if r == nil {
		return false
	}
	return r.healthy.Load()
}

// SetHealthy updates the health flag.
func (r *InMemoryRedis) SetHealthy(v bool) {
	if r == nil {
		return
	}
	r.healthy.Store(v)
}

// Pipeline creates a new pipeline.
func (r *InMemoryRedis) Pipeline() RedisPipeline {
	if r == nil {
		return &InMemoryPipeline{}
	}
	return &InMemoryPipeline{redis: r}
}

// ExecTokenBucket executes a token bucket decision.
func (r *InMemoryRedis) ExecTokenBucket(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execTokenBucketLocked(key, params, cost)
}

// ExecFixedWindow executes a fixed window decision.
func (r *InMemoryRedis) ExecFixedWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execFixedWindowLocked(key, params, cost)
}

// ExecSlidingWindow executes a sliding window decision.
func (r *InMemoryRedis) ExecSlidingWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execSlidingWindowLocked(key, params, cost)
}

type redisOpKind int

const (
	opTokenBucket redisOpKind = iota
	opFixedWindow
	opSlidingWindow
)

type redisOp struct {
	kind   redisOpKind
	key    string
	params RuleParams
	cost   int64
}

// InMemoryPipeline queues operations for execution.
type InMemoryPipeline struct {
	redis *InMemoryRedis
	ops   []redisOp
}

// ExecTokenBucket queues a token bucket operation.
func (p *InMemoryPipeline) ExecTokenBucket(key string, params RuleParams, cost int64) {
	p.ops = append(p.ops, redisOp{kind: opTokenBucket, key: key, params: params, cost: cost})
}

// ExecFixedWindow queues a fixed window operation.
func (p *InMemoryPipeline) ExecFixedWindow(key string, params RuleParams, cost int64) {
	p.ops = append(p.ops, redisOp{kind: opFixedWindow, key: key, params: params, cost: cost})
}

// ExecSlidingWindow queues a sliding window operation.
func (p *InMemoryPipeline) ExecSlidingWindow(key string, params RuleParams, cost int64) {
	p.ops = append(p.ops, redisOp{kind: opSlidingWindow, key: key, params: params, cost: cost})
}

// Exec runs queued operations.
func (p *InMemoryPipeline) Exec(ctx context.Context) ([]*Decision, error) {
	if p.redis == nil {
		return nil, errors.New("redis is nil")
	}
	r := p.redis
	r.mu.Lock()
	defer r.mu.Unlock()

	decisions := make([]*Decision, len(p.ops))
	for i, op := range p.ops {
		var (
			decision *Decision
			err      error
		)
		switch op.kind {
		case opTokenBucket:
			decision, err = r.execTokenBucketLocked(op.key, op.params, op.cost)
		case opFixedWindow:
			decision, err = r.execFixedWindowLocked(op.key, op.params, op.cost)
		case opSlidingWindow:
			decision, err = r.execSlidingWindowLocked(op.key, op.params, op.cost)
		}
		if err != nil {
			return nil, err
		}
		decisions[i] = decision
	}
	return decisions, nil
}

func (r *InMemoryRedis) execTokenBucketLocked(key string, params RuleParams, cost int64) (*Decision, error) {
	if !r.healthy.Load() {
		return nil, errors.New("redis unhealthy")
	}
	if cost <= 0 || params.Limit <= 0 {
		return nil, errors.New("invalid cost or limit")
	}
	window := params.Window
	if window <= 0 {
		window = r.windowDefault
	}
	capacity := params.Limit
	if params.Burst > capacity {
		capacity = params.Burst
	}
	rate := float64(params.Limit) / window.Seconds()
	now := r.now()
	state := r.tokenBuckets[key]
	if state == nil {
		state = &tbState{tokens: float64(capacity), last: now}
		r.tokenBuckets[key] = state
	}
	elapsed := now.Sub(state.last).Seconds()
	if elapsed > 0 {
		state.tokens = math.Min(float64(capacity), state.tokens+elapsed*rate)
	}
	state.last = now
	allowed := float64(cost) <= state.tokens
	if allowed {
		state.tokens -= float64(cost)
	}
	remaining := int64(math.Floor(state.tokens))
	retryAfter := time.Duration(0)
	if !allowed && rate > 0 {
		needed := float64(cost) - state.tokens
		if needed < 0 {
			needed = 0
		}
		retryAfter = time.Duration(needed / rate * float64(time.Second))
	}
	resetAfter := time.Duration(0)
	if rate > 0 {
		resetAfter = time.Duration((float64(capacity) - state.tokens) / rate * float64(time.Second))
	}
	return &Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		Limit:      params.Limit,
		ResetAfter: resetAfter,
		RetryAfter: retryAfter,
	}, nil
}

func (r *InMemoryRedis) execFixedWindowLocked(key string, params RuleParams, cost int64) (*Decision, error) {
	if !r.healthy.Load() {
		return nil, errors.New("redis unhealthy")
	}
	if cost <= 0 || params.Limit <= 0 {
		return nil, errors.New("invalid cost or limit")
	}
	window := params.Window
	if window <= 0 {
		window = r.windowDefault
	}
	now := r.now()
	windowStart := now.Truncate(window)
	state := r.fixedWindows[key]
	if state == nil {
		state = &fwState{windowStart: windowStart}
		r.fixedWindows[key] = state
	}
	if state.windowStart != windowStart {
		state.windowStart = windowStart
		state.used = 0
	}
	allowed := state.used+cost <= params.Limit
	if allowed {
		state.used += cost
	}
	remaining := params.Limit - state.used
	if remaining < 0 {
		remaining = 0
	}
	resetAfter := windowStart.Add(window).Sub(now)
	if resetAfter < 0 {
		resetAfter = 0
	}
	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = resetAfter
	}
	return &Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		Limit:      params.Limit,
		ResetAfter: resetAfter,
		RetryAfter: retryAfter,
	}, nil
}

func (r *InMemoryRedis) execSlidingWindowLocked(key string, params RuleParams, cost int64) (*Decision, error) {
	if !r.healthy.Load() {
		return nil, errors.New("redis unhealthy")
	}
	if cost <= 0 || params.Limit <= 0 {
		return nil, errors.New("invalid cost or limit")
	}
	window := params.Window
	if window <= 0 {
		window = r.windowDefault
	}
	now := r.now()
	state := r.slidingWindows[key]
	if state == nil {
		state = &swState{window: window}
		r.slidingWindows[key] = state
	}
	if state.window != window {
		state.window = window
		state.events = nil
	}
	cutoff := now.Add(-window)
	var total int64
	filtered := state.events[:0]
	for _, event := range state.events {
		if event.t.After(cutoff) || event.t.Equal(cutoff) {
			filtered = append(filtered, event)
			total += event.cost
		}
	}
	state.events = filtered
	allowed := total+cost <= params.Limit
	if allowed {
		state.events = append(state.events, swEvent{t: now, cost: cost})
		total += cost
	}
	remaining := params.Limit - total
	if remaining < 0 {
		remaining = 0
	}
	retryAfter := time.Duration(0)
	if !allowed && len(state.events) > 0 {
		retryAfter = state.events[0].t.Add(window).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
	}
	return &Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		Limit:      params.Limit,
		ResetAfter: window,
		RetryAfter: retryAfter,
	}, nil
}
