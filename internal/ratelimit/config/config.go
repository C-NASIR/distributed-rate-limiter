// Package config provides configuration for the application wiring.
package config

import (
	"time"

	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/observability"
)

// Config captures dependency and runtime settings.
type Config struct {
	Region                     string
	RegionGroup                string
	EnableGlobalOwnership      bool
	GlobalOwnershipFallback    bool
	RegionInstanceWeight       int
	DegradeRequireRegionQuorum bool
	RegionQuorumFraction       float64
	Redis                      core.RedisClient
	RuleDB                     core.RuleDB
	Outbox                     core.Outbox
	PubSub                     core.PubSub
	Membership                 core.Membership
	Channel                    string
	HTTPListenAddr             string
	EnableHTTP                 bool
	EnableGRPC                 bool
	GRPCListenAddr             string
	GRPCKeepAlive              time.Duration
	TraceSampleRate            int
	CoalesceEnabled            bool
	CoalesceTTL                time.Duration
	CoalesceShards             int
	BreakerOptions             core.CircuitOptions
	CacheSyncInterval          time.Duration
	HealthInterval             time.Duration
	LimiterPolicy              core.LimiterPolicy
	FallbackPolicy             core.FallbackPolicy
	DegradeThresh              core.DegradeThresholds
	Tracer                     observability.Tracer
	Sampler                    observability.Sampler
	Metrics                    observability.Metrics
	HTTPReadTimeout            time.Duration
	HTTPWriteTimeout           time.Duration
	HTTPIdleTimeout            time.Duration
	RequestTimeout             time.Duration
	DrainTimeout               time.Duration
	MaxBodyBytes               int64
	EnableAuth                 bool
	AdminToken                 string
	Logger                     observability.Logger
}
