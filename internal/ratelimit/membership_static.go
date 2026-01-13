// Package ratelimit provides static membership.
package ratelimit

import (
	"context"
	"sync/atomic"
)

// StaticMembership provides fixed membership information.
type StaticMembership struct {
	self       string
	selfRegion string
	instances  []InstanceInfo
	healthy    atomic.Bool
}

// NewStaticMembership constructs a StaticMembership.
func NewStaticMembership(selfID string, selfRegion string, instances []InstanceInfo) *StaticMembership {
	if len(instances) == 0 {
		instances = []InstanceInfo{{ID: selfID, Region: selfRegion, Weight: 1}}
	}
	for i := range instances {
		if instances[i].Weight <= 0 {
			instances[i].Weight = 1
		}
	}
	m := &StaticMembership{self: selfID, selfRegion: selfRegion, instances: instances}
	m.healthy.Store(true)
	return m
}

// NewSingleInstanceMembership constructs a single instance membership.
func NewSingleInstanceMembership(selfID string, region string) *StaticMembership {
	return NewStaticMembership(selfID, region, []InstanceInfo{{ID: selfID, Region: region, Weight: 1}})
}

// SelfID returns the instance ID.
func (m *StaticMembership) SelfID() string {
	if m == nil {
		return ""
	}
	return m.self
}

// SelfRegion returns the instance region.
func (m *StaticMembership) SelfRegion() string {
	if m == nil {
		return ""
	}
	return m.selfRegion
}

// Instances returns the known instances.
func (m *StaticMembership) Instances(ctx context.Context) ([]InstanceInfo, error) {
	if m == nil {
		return nil, nil
	}
	return append([]InstanceInfo(nil), m.instances...), nil
}

// Healthy reports membership health.
func (m *StaticMembership) Healthy() bool {
	if m == nil {
		return false
	}
	return m.healthy.Load()
}

// SetHealthy updates membership health.
func (m *StaticMembership) SetHealthy(v bool) {
	if m == nil {
		return
	}
	m.healthy.Store(v)
}

// SetSelfWeight updates the weight for the local instance.
func (m *StaticMembership) SetSelfWeight(weight int) {
	if m == nil {
		return
	}
	if weight <= 0 {
		weight = 1
	}
	for i := range m.instances {
		if m.instances[i].ID == m.self {
			m.instances[i].Weight = weight
			return
		}
	}
	m.instances = append(m.instances, InstanceInfo{ID: m.self, Region: m.selfRegion, Weight: weight})
}
