package core_test

import (
	"context"
	"errors"
	"sync/atomic"

	"ratelimit/internal/ratelimit/core"
)

type testRedis struct {
	healthy atomic.Bool
}

func newTestRedis() *testRedis {
	redis := &testRedis{}
	redis.healthy.Store(true)
	return redis
}

func (r *testRedis) Healthy(ctx context.Context) bool {
	if r == nil {
		return false
	}
	return r.healthy.Load()
}

func (r *testRedis) SetHealthy(v bool) {
	if r == nil {
		return
	}
	r.healthy.Store(v)
}

func (r *testRedis) Pipeline() core.RedisPipeline {
	return &testRedisPipeline{redis: r}
}

func (r *testRedis) ExecTokenBucket(ctx context.Context, key string, params core.RuleParams, cost int64) (*core.Decision, error) {
	return r.exec(ctx, params, cost)
}

func (r *testRedis) ExecFixedWindow(ctx context.Context, key string, params core.RuleParams, cost int64) (*core.Decision, error) {
	return r.exec(ctx, params, cost)
}

func (r *testRedis) ExecSlidingWindow(ctx context.Context, key string, params core.RuleParams, cost int64) (*core.Decision, error) {
	return r.exec(ctx, params, cost)
}

func (r *testRedis) exec(ctx context.Context, params core.RuleParams, cost int64) (*core.Decision, error) {
	if !r.Healthy(ctx) {
		return nil, errors.New("redis unhealthy")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 1
	}
	allowed := cost > 0
	remaining := limit - cost
	if remaining < 0 {
		remaining = 0
	}
	return &core.Decision{Allowed: allowed, Remaining: remaining, Limit: limit}, nil
}

type testRedisPipeline struct {
	redis  *testRedis
	params []core.RuleParams
	costs  []int64
}

func (p *testRedisPipeline) ExecTokenBucket(key string, params core.RuleParams, cost int64) {
	p.params = append(p.params, params)
	p.costs = append(p.costs, cost)
}

func (p *testRedisPipeline) ExecFixedWindow(key string, params core.RuleParams, cost int64) {
	p.ExecTokenBucket(key, params, cost)
}

func (p *testRedisPipeline) ExecSlidingWindow(key string, params core.RuleParams, cost int64) {
	p.ExecTokenBucket(key, params, cost)
}

func (p *testRedisPipeline) Exec(ctx context.Context) ([]*core.Decision, error) {
	if p.redis == nil {
		return nil, errors.New("redis is nil")
	}
	decisions := make([]*core.Decision, len(p.params))
	for i, params := range p.params {
		cost := int64(0)
		if i < len(p.costs) {
			cost = p.costs[i]
		}
		decision, err := p.redis.exec(ctx, params, cost)
		if err != nil {
			return nil, err
		}
		decisions[i] = decision
	}
	return decisions, nil
}
