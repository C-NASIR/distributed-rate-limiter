// Package ratelimit provides configuration for the application wiring.
package ratelimit

import "time"

// Config captures dependency and runtime settings.
type Config struct {
	Region            string
	Redis             RedisClient
	RuleDB            RuleDB
	Outbox            Outbox
	PubSub            PubSub
	Membership        Membership
	Channel           string
	HTTPListenAddr    string
	EnableHTTP        bool
	TraceSampleRate   int
	CoalesceEnabled   bool
	CoalesceTTL       time.Duration
	CoalesceShards    int
	BreakerOptions    CircuitOptions
	CacheSyncInterval time.Duration
	HealthInterval    time.Duration
	LimiterPolicy     LimiterPolicy
	FallbackPolicy    FallbackPolicy
	DegradeThresh     DegradeThresholds
	Tracer            Tracer
	Sampler           Sampler
	Metrics           Metrics
}
