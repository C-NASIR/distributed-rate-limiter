// Package ratelimit provides limiter primitives.
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	CreateFunc func(rule *Rule) (Limiter, RuleParams, error)
}

// Create builds a limiter from a rule.
func (factory *LimiterFactory) Create(rule *Rule) (Limiter, RuleParams, error) {
	if factory == nil {
		return nil, RuleParams{}, errors.New("limiter factory is nil")
	}
	if factory.CreateFunc != nil {
		return factory.CreateFunc(rule)
	}
	if rule == nil {
		return nil, RuleParams{}, errors.New("rule is required")
	}
	params := RuleParams{
		Limit:   rule.Limit,
		Window:  rule.Window,
		Burst:   rule.BurstSize,
		Version: rule.Version,
	}
	return newStubLimiter(params), params, nil
}

type stubLimiter struct {
	params RuleParams
	closed atomic.Bool
	once   sync.Once
}

func newStubLimiter(params RuleParams) *stubLimiter {
	return &stubLimiter{params: params}
}

func (lim *stubLimiter) Allow(ctx context.Context, key []byte, cost int64) (*Decision, error) {
	return &Decision{
		Allowed:   true,
		Remaining: lim.params.Limit,
		Limit:     lim.params.Limit,
	}, nil
}

func (lim *stubLimiter) AllowBatch(ctx context.Context, keys [][]byte, costs []int64) ([]*Decision, error) {
	decisions := make([]*Decision, len(keys))
	for i := range keys {
		decisions[i] = &Decision{
			Allowed:   true,
			Remaining: lim.params.Limit,
			Limit:     lim.params.Limit,
		}
	}
	return decisions, nil
}

func (lim *stubLimiter) RuleVersion() int64 {
	return lim.params.Version
}

func (lim *stubLimiter) Close() error {
	lim.once.Do(func() {
		lim.closed.Store(true)
	})
	return nil
}
