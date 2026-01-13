// Package ratelimit provides static membership.
package ratelimit

import (
	"context"
	"sync/atomic"
)

// StaticMembership provides fixed membership information.
type StaticMembership struct {
	self      string
	instances []string
	healthy   atomic.Bool
}

// NewStaticMembership constructs a StaticMembership.
func NewStaticMembership(self string, instances []string) *StaticMembership {
	if len(instances) == 0 {
		instances = []string{self}
	}
	m := &StaticMembership{self: self, instances: instances}
	m.healthy.Store(true)
	return m
}

// SelfID returns the instance ID.
func (m *StaticMembership) SelfID() string {
	if m == nil {
		return ""
	}
	return m.self
}

// Instances returns the known instances.
func (m *StaticMembership) Instances(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, nil
	}
	return append([]string(nil), m.instances...), nil
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
