// Package core provides operating mode controls.
package core

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"ratelimit/internal/ratelimit/observability"
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
	SelfRegion() string
	Instances(ctx context.Context) ([]InstanceInfo, error)
	Healthy() bool
}

// InstanceInfo captures membership instance data.
type InstanceInfo struct {
	ID     string
	Region string
	Weight int
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
	region           string
	requireQuorum    bool
	quorumFraction   float64
	lastRedisHealthy atomic.Int64
	lastMshipHealthy atomic.Int64
	logger           observability.Logger
	lastMode         atomic.Int32
}

// NewDegradeController constructs a DegradeController.
func NewDegradeController(redis RedisClient, mship Membership, th DegradeThresholds, region string, requireQuorum bool, quorumFraction float64) *DegradeController {
	if th.RedisUnhealthyFor == 0 {
		th.RedisUnhealthyFor = 500 * time.Millisecond
	}
	if th.MembershipUnhealthy == 0 {
		th.MembershipUnhealthy = 2 * time.Second
	}
	if quorumFraction == 0 {
		quorumFraction = 0.5
	}

	now := time.Now().UnixNano()
	controller := &DegradeController{
		redis:          redis,
		mship:          mship,
		thresholds:     th,
		region:         region,
		requireQuorum:  requireQuorum,
		quorumFraction: quorumFraction,
	}
	controller.mode.Store(int32(ModeNormal))
	controller.lastMode.Store(int32(ModeNormal))
	controller.lastRedisHealthy.Store(now)
	controller.lastMshipHealthy.Store(now)
	return controller
}

// SetLogger configures a logger for mode changes.
func (dc *DegradeController) SetLogger(l observability.Logger) {
	if dc == nil {
		return
	}
	dc.logger = l
}

// Mode returns the current operating mode.
func (dc *DegradeController) Mode() OperatingMode {
	if dc == nil {
		return ModeNormal
	}
	return OperatingMode(dc.mode.Load())
}

// RegionStatus returns regional membership counts and quorum status.
func (dc *DegradeController) RegionStatus(ctx context.Context) (inRegion int, total int, ok bool) {
	if dc == nil || dc.mship == nil {
		if dc != nil && !dc.requireQuorum {
			return 0, 0, true
		}
		return 0, 0, false
	}
	if !dc.requireQuorum {
		instances, err := dc.mship.Instances(ctx)
		if err != nil {
			return 0, 0, true
		}
		for _, instance := range instances {
			if instance.Region == dc.region {
				inRegion++
			}
		}
		return inRegion, len(instances), true
	}
	instances, err := dc.mship.Instances(ctx)
	if err != nil {
		return 0, 0, false
	}
	for _, instance := range instances {
		if instance.Region == dc.region {
			inRegion++
		}
	}
	total = len(instances)
	if total == 0 {
		return inRegion, total, false
	}
	required := int(math.Ceil(float64(total) * dc.quorumFraction))
	if required < 1 {
		required = 1
	}
	return inRegion, total, inRegion >= required
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
	if dc.requireQuorum {
		_, _, ok := dc.RegionStatus(ctx)
		if !ok {
			mshipHealthy = false
		}
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
	prev := OperatingMode(dc.lastMode.Load())
	if prev != mode {
		dc.lastMode.Store(int32(mode))
		if dc.logger != nil {
			dc.logger.Info("mode changed", map[string]any{
				"old":       modeLabel(prev),
				"new":       modeLabel(mode),
				"timestamp": now.UnixNano(),
			})
		}
	}
}

func modeLabel(mode OperatingMode) string {
	switch mode {
	case ModeDegraded:
		return "degraded"
	case ModeEmergency:
		return "emergency"
	default:
		return "normal"
	}
}
