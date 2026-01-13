// Package ratelimit provides configuration for the application wiring.
package ratelimit

import "time"

// Config captures dependency and runtime settings.
type Config struct {
	Region                     string
	RegionGroup                string
	EnableGlobalOwnership      bool
	GlobalOwnershipFallback    bool
	RegionInstanceWeight       int
	DegradeRequireRegionQuorum bool
	RegionQuorumFraction       float64
	Redis                      RedisClient
	RuleDB                     RuleDB
	Outbox                     Outbox
	PubSub                     PubSub
	Membership                 Membership
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
	BreakerOptions             CircuitOptions
	CacheSyncInterval          time.Duration
	HealthInterval             time.Duration
	LimiterPolicy              LimiterPolicy
	FallbackPolicy             FallbackPolicy
	DegradeThresh              DegradeThresholds
	Tracer                     Tracer
	Sampler                    Sampler
	Metrics                    Metrics
	HTTPReadTimeout            time.Duration
	HTTPWriteTimeout           time.Duration
	HTTPIdleTimeout            time.Duration
	RequestTimeout             time.Duration
	DrainTimeout               time.Duration
	MaxBodyBytes               int64
	EnableAuth                 bool
	AdminToken                 string
	Logger                     Logger
}
