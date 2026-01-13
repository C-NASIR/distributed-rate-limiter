// Package ratelimit provides ownership checks.
package ratelimit

import (
	"context"
	"hash/fnv"
)

// Ownership checks ownership for a key.
type Ownership interface {
	IsOwner(ctx context.Context, key []byte) bool
}

// RendezvousOwnership implements rendezvous hashing ownership.
type RendezvousOwnership struct {
	m              Membership
	region         string
	enableGlobal   bool
	globalFallback bool
}

// NewRendezvousOwnership constructs a RendezvousOwnership.
func NewRendezvousOwnership(m Membership, region string, enableGlobal bool, globalFallback bool) *RendezvousOwnership {
	return &RendezvousOwnership{m: m, region: region, enableGlobal: enableGlobal, globalFallback: globalFallback}
}

// IsOwner returns true when the local instance owns the key.
func (ro *RendezvousOwnership) IsOwner(ctx context.Context, key []byte) bool {
	if ro == nil || ro.m == nil {
		return true
	}
	instances, err := ro.m.Instances(ctx)
	if err != nil || len(instances) == 0 {
		return true
	}
	self := ro.m.SelfID()
	if self == "" {
		return true
	}
	region := ro.region
	filtered := make([]InstanceInfo, 0, len(instances))
	if ro.enableGlobal || region == "" {
		filtered = instances
	} else {
		for _, instance := range instances {
			if instance.Region == region {
				filtered = append(filtered, instance)
			}
		}
	}
	if len(filtered) == 0 {
		if ro.globalFallback {
			filtered = instances
		} else {
			if ro.m.SelfRegion() == region && len(instances) == 1 && instances[0].ID == self {
				return true
			}
			return true
		}
	}

	var bestID string
	var bestScore uint64
	for i, instance := range filtered {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(instance.ID))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(key)
		score := hasher.Sum64()
		weight := instance.Weight
		if weight < 1 {
			weight = 1
		}
		score *= uint64(weight)
		if i == 0 || score > bestScore {
			bestScore = score
			bestID = instance.ID
		}
	}
	if bestID == "" {
		return true
	}
	return bestID == self
}
