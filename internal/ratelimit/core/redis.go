// Package core defines redis interfaces.
package core

import "context"

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
