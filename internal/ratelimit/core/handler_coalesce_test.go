package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimitHandler_CoalescesHotKey(t *testing.T) {
	t.Parallel()

	redis := &countingRedis{}
	membership := NewSingleInstanceMembership("self", "region")
	degrade := NewDegradeController(redis, membership, DegradeThresholds{}, "region", false, 0)
	fallback := &FallbackLimiter{
		ownership:   NewRendezvousOwnership(membership, "region", false, true),
		mship:       membership,
		region:      "region",
		regionGroup: "region",
		policy:      NormalizeFallbackPolicy(FallbackPolicy{}),
		mode:        degrade,
		local:       &LocalLimiterStore{},
	}
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: degrade, breaker: NewCircuitBreaker(CircuitOptions{})}
	rules := NewRuleCache()
	rules.ReplaceAll([]*Rule{{TenantID: "tenant", Resource: "resource", Algorithm: "token_bucket", Limit: 10, Window: time.Second, Version: 1}})
	pool := NewLimiterPool(rules, factory, LimiterPolicy{Shards: 1, MaxEntriesShard: 4})
	keys := &KeyBuilder{bufPool: NewByteBufferPool(64)}
	respPool := NewResponsePool()
	coalescer := NewCoalescer(1, 50*time.Millisecond)
	handler := NewRateLimitHandler(rules, pool, keys, "region", respPool, nil, nil, nil, coalescer)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := handler.CheckLimit(context.Background(), &CheckLimitRequest{
				TenantID: "tenant",
				UserID:   "user",
				Resource: "resource",
				Cost:     1,
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if resp.ErrorCode != "" {
				t.Errorf("unexpected response: %#v", resp)
			}
			handler.ReleaseResponse(resp)
		}()
	}
	wg.Wait()
	if got := redis.count.Load(); got != 1 {
		t.Fatalf("expected 1 redis call got %d", got)
	}
}

type countingRedis struct {
	count atomic.Int64
}

func (r *countingRedis) Healthy(ctx context.Context) bool {
	return true
}

func (r *countingRedis) Pipeline() RedisPipeline {
	return &countingPipeline{redis: r}
}

func (r *countingRedis) ExecTokenBucket(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	r.count.Add(1)
	time.Sleep(20 * time.Millisecond)
	return &Decision{Allowed: true, Remaining: params.Limit - cost, Limit: params.Limit}, nil
}

func (r *countingRedis) ExecFixedWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	return r.ExecTokenBucket(ctx, key, params, cost)
}

func (r *countingRedis) ExecSlidingWindow(ctx context.Context, key string, params RuleParams, cost int64) (*Decision, error) {
	return r.ExecTokenBucket(ctx, key, params, cost)
}

type countingPipeline struct {
	redis  *countingRedis
	keys   []string
	params []RuleParams
	costs  []int64
}

func (p *countingPipeline) ExecTokenBucket(key string, params RuleParams, cost int64) {
	p.keys = append(p.keys, key)
	p.params = append(p.params, params)
	p.costs = append(p.costs, cost)
}

func (p *countingPipeline) ExecFixedWindow(key string, params RuleParams, cost int64) {
	p.ExecTokenBucket(key, params, cost)
}

func (p *countingPipeline) ExecSlidingWindow(key string, params RuleParams, cost int64) {
	p.ExecTokenBucket(key, params, cost)
}

func (p *countingPipeline) Exec(ctx context.Context) ([]*Decision, error) {
	decisions := make([]*Decision, len(p.keys))
	for i, key := range p.keys {
		decision, err := p.redis.ExecTokenBucket(ctx, key, p.params[i], p.costs[i])
		if err != nil {
			return nil, err
		}
		decisions[i] = decision
	}
	return decisions, nil
}
