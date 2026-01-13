// Package ratelimit provides operating mode controls.
package ratelimit

import (
	"context"
	"sync/atomic"
	"time"
)

// OperatingMode represents the current operating state.
type OperatingMode int32

const (
	ModeNormal OperatingMode = iota
	ModeDegraded
	ModeEmergency
)

// Membership provides instance membership information.
type Membership interface {
	SelfID() string
	Instances(ctx context.Context) ([]string, error)
	Healthy() bool
}

// DegradeThresholds defines thresholds for mode switching.
type DegradeThresholds struct {
	RedisUnhealthyFor   time.Duration
	MembershipUnhealthy time.Duration
	ErrorRateWindow     time.Duration
}

// DegradeController tracks system health for mode switching.
type DegradeController struct {
	mode             atomic.Int32
	redis            RedisClient
	mship            Membership
	thresholds       DegradeThresholds
	lastRedisHealthy atomic.Int64
	lastMshipHealthy atomic.Int64
}

// NewDegradeController constructs a DegradeController.
func NewDegradeController(redis RedisClient, mship Membership, th DegradeThresholds) *DegradeController {
	if th.RedisUnhealthyFor == 0 {
		th.RedisUnhealthyFor = 500 * time.Millisecond
	}
	if th.MembershipUnhealthy == 0 {
		th.MembershipUnhealthy = 2 * time.Second
	}

	now := time.Now().UnixNano()
	controller := &DegradeController{
		redis:      redis,
		mship:      mship,
		thresholds: th,
	}
	controller.mode.Store(int32(ModeNormal))
	controller.lastRedisHealthy.Store(now)
	controller.lastMshipHealthy.Store(now)
	return controller
}

// Mode returns the current operating mode.
func (dc *DegradeController) Mode() OperatingMode {
	if dc == nil {
		return ModeNormal
	}
	return OperatingMode(dc.mode.Load())
}

// Update refreshes the current operating mode.
func (dc *DegradeController) Update(ctx context.Context) {
	if dc == nil {
		return
	}
	now := time.Now()
	redisHealthy := true
	if dc.redis != nil {
		redisHealthy = dc.redis.Healthy(ctx)
	}
	mshipHealthy := true
	if dc.mship != nil {
		mshipHealthy = dc.mship.Healthy()
	}
	if redisHealthy {
		dc.lastRedisHealthy.Store(now.UnixNano())
	}
	if mshipHealthy {
		dc.lastMshipHealthy.Store(now.UnixNano())
	}

	redisAge := now.Sub(time.Unix(0, dc.lastRedisHealthy.Load()))
	mshipAge := now.Sub(time.Unix(0, dc.lastMshipHealthy.Load()))

	mode := ModeNormal
	if redisAge >= dc.thresholds.RedisUnhealthyFor {
		mode = ModeDegraded
		if mshipAge >= dc.thresholds.MembershipUnhealthy {
			mode = ModeEmergency
		}
	}
	dc.mode.Store(int32(mode))
}
