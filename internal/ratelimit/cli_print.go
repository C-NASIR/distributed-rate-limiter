// Package ratelimit provides CLI helpers.
package ratelimit

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
)

// PrintConfig writes the config to the writer as JSON.
func PrintConfig(w io.Writer, cfg *Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if w == nil {
		return errors.New("writer is required")
	}
	snapshot := newConfigSnapshot(cfg)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

type durationMillis time.Duration

func (d durationMillis) MarshalJSON() ([]byte, error) {
	ms := time.Duration(d).Milliseconds()
	return []byte(strconv.FormatInt(ms, 10)), nil
}

type configSnapshot struct {
	Region                     string
	RegionGroup                string
	EnableGlobalOwnership      bool
	GlobalOwnershipFallback    bool
	RegionInstanceWeight       int
	DegradeRequireRegionQuorum bool
	RegionQuorumFraction       float64
	Channel                    string
	HTTPListenAddr             string
	EnableHTTP                 bool
	EnableGRPC                 bool
	GRPCListenAddr             string
	GRPCKeepAlive              durationMillis
	TraceSampleRate            int
	CoalesceEnabled            bool
	CoalesceTTL                durationMillis
	CoalesceShards             int
	BreakerOptions             circuitOptionsSnapshot
	CacheSyncInterval          durationMillis
	HealthInterval             durationMillis
	LimiterPolicy              limiterPolicySnapshot
	FallbackPolicy             FallbackPolicy
	DegradeThresh              degradeThresholdSnapshot
	HTTPReadTimeout            durationMillis
	HTTPWriteTimeout           durationMillis
	HTTPIdleTimeout            durationMillis
	RequestTimeout             durationMillis
	DrainTimeout               durationMillis
	MaxBodyBytes               int64
	EnableAuth                 bool
	AdminToken                 string
}

type circuitOptionsSnapshot struct {
	FailureThreshold int64
	OpenDuration     durationMillis
	HalfOpenMaxCalls int64
}

type limiterPolicySnapshot struct {
	Shards          int
	MaxEntriesShard int
	QuiesceWindow   durationMillis
	CloseTimeout    durationMillis
}

type degradeThresholdSnapshot struct {
	RedisUnhealthyFor   durationMillis
	MembershipUnhealthy durationMillis
	ErrorRateWindow     durationMillis
}

func newConfigSnapshot(cfg *Config) configSnapshot {
	snapshot := configSnapshot{}
	if cfg == nil {
		return snapshot
	}
	snapshot.Region = cfg.Region
	snapshot.RegionGroup = cfg.RegionGroup
	snapshot.EnableGlobalOwnership = cfg.EnableGlobalOwnership
	snapshot.GlobalOwnershipFallback = cfg.GlobalOwnershipFallback
	snapshot.RegionInstanceWeight = cfg.RegionInstanceWeight
	snapshot.DegradeRequireRegionQuorum = cfg.DegradeRequireRegionQuorum
	snapshot.RegionQuorumFraction = cfg.RegionQuorumFraction
	snapshot.Channel = cfg.Channel
	snapshot.HTTPListenAddr = cfg.HTTPListenAddr
	snapshot.EnableHTTP = cfg.EnableHTTP
	snapshot.EnableGRPC = cfg.EnableGRPC
	snapshot.GRPCListenAddr = cfg.GRPCListenAddr
	snapshot.GRPCKeepAlive = durationMillis(cfg.GRPCKeepAlive)
	snapshot.TraceSampleRate = cfg.TraceSampleRate
	snapshot.CoalesceEnabled = cfg.CoalesceEnabled
	snapshot.CoalesceTTL = durationMillis(cfg.CoalesceTTL)
	snapshot.CoalesceShards = cfg.CoalesceShards
	snapshot.BreakerOptions = circuitOptionsSnapshot{
		FailureThreshold: cfg.BreakerOptions.FailureThreshold,
		OpenDuration:     durationMillis(cfg.BreakerOptions.OpenDuration),
		HalfOpenMaxCalls: cfg.BreakerOptions.HalfOpenMaxCalls,
	}
	snapshot.CacheSyncInterval = durationMillis(cfg.CacheSyncInterval)
	snapshot.HealthInterval = durationMillis(cfg.HealthInterval)
	snapshot.LimiterPolicy = limiterPolicySnapshot{
		Shards:          cfg.LimiterPolicy.Shards,
		MaxEntriesShard: cfg.LimiterPolicy.MaxEntriesShard,
		QuiesceWindow:   durationMillis(cfg.LimiterPolicy.QuiesceWindow),
		CloseTimeout:    durationMillis(cfg.LimiterPolicy.CloseTimeout),
	}
	snapshot.FallbackPolicy = cfg.FallbackPolicy
	snapshot.DegradeThresh = degradeThresholdSnapshot{
		RedisUnhealthyFor:   durationMillis(cfg.DegradeThresh.RedisUnhealthyFor),
		MembershipUnhealthy: durationMillis(cfg.DegradeThresh.MembershipUnhealthy),
		ErrorRateWindow:     durationMillis(cfg.DegradeThresh.ErrorRateWindow),
	}
	snapshot.HTTPReadTimeout = durationMillis(cfg.HTTPReadTimeout)
	snapshot.HTTPWriteTimeout = durationMillis(cfg.HTTPWriteTimeout)
	snapshot.HTTPIdleTimeout = durationMillis(cfg.HTTPIdleTimeout)
	snapshot.RequestTimeout = durationMillis(cfg.RequestTimeout)
	snapshot.DrainTimeout = durationMillis(cfg.DrainTimeout)
	snapshot.MaxBodyBytes = cfg.MaxBodyBytes
	snapshot.EnableAuth = cfg.EnableAuth
	snapshot.AdminToken = cfg.AdminToken
	return snapshot
}
