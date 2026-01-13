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
	m Membership
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

	var bestID string
	var bestScore uint64
	for i, instance := range instances {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(instance))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(key)
		score := hasher.Sum64()
		if i == 0 || score > bestScore {
			bestScore = score
			bestID = instance
		}
	}
	if bestID == "" {
		return true
	}
	return bestID == self
}
