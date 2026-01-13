// Package ratelimit provides fallback limiting.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// FallbackPolicy controls fallback limiting behavior.
type FallbackPolicy struct {
	LocalCapPerWindow      int64
	DenyWhenNotOwner       bool
	EmergencyAllowSmallCap bool
	EmergencyCapPerWindow  int64
}

// LocalLimiterStore provides local fixed window counters.
type LocalLimiterStore struct {
	mu      sync.Mutex
	windows map[string]*windowCounter
}

type windowCounter struct {
	windowStart time.Time
	used        int64
}

// AllowFixedWindow evaluates a fixed window counter.
func (store *LocalLimiterStore) AllowFixedWindow(key string, cap int64, window time.Duration, cost int64, now time.Time) (bool, int64, time.Duration, time.Duration) {
	if cost <= 0 || cap <= 0 {
		return false, 0, 0, 0
	}
	if window <= 0 {
		window = time.Second
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.windows == nil {
		store.windows = make(map[string]*windowCounter)
	}
	windowStart := now.Truncate(window)
	counter := store.windows[key]
	if counter == nil {
		counter = &windowCounter{windowStart: windowStart}
		store.windows[key] = counter
	}
	if counter.windowStart != windowStart {
		counter.windowStart = windowStart
		counter.used = 0
	}
	allowed := counter.used+cost <= cap
	if allowed {
		counter.used += cost
	}
	remaining := cap - counter.used
	if remaining < 0 {
		remaining = 0
	}
	resetAfter := windowStart.Add(window).Sub(now)
	if resetAfter < 0 {
		resetAfter = 0
	}
	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = resetAfter
	}
	return allowed, remaining, resetAfter, retryAfter
}

// FallbackLimiter applies local fallback limiting.
type FallbackLimiter struct {
	ownership Ownership
	policy    FallbackPolicy
	mode      *DegradeController
	local     *LocalLimiterStore
}

// Allow applies fallback limiting.
func (fl *FallbackLimiter) Allow(ctx context.Context, key []byte, params RuleParams, cost int64) *Decision {
	if fl == nil {
		return &Decision{Allowed: false}
	}
	policy := normalizeFallbackPolicy(fl.policy)
	mode := ModeNormal
	if fl.mode != nil {
		mode = fl.mode.Mode()
	}

	cap := policy.LocalCapPerWindow
	if mode == ModeEmergency {
		if policy.EmergencyAllowSmallCap {
			cap = policy.EmergencyCapPerWindow
		} else {
			return &Decision{Allowed: false, Limit: 0}
		}
	}

	if policy.DenyWhenNotOwner {
		if fl.ownership != nil && !fl.ownership.IsOwner(ctx, key) {
			return &Decision{Allowed: false, Limit: cap}
		}
	}
	window := params.Window
	if window <= 0 {
		window = time.Second
	}
	store := fl.local
	if store == nil {
		store = &LocalLimiterStore{}
		fl.local = store
	}
	allowed, remaining, resetAfter, retryAfter := store.AllowFixedWindow(string(key), cap, window, cost, time.Now())
	return &Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		Limit:      cap,
		ResetAfter: resetAfter,
		RetryAfter: retryAfter,
	}
}

func normalizeFallbackPolicy(policy FallbackPolicy) FallbackPolicy {
	if policy == (FallbackPolicy{}) {
		return FallbackPolicy{
			LocalCapPerWindow:      100,
			DenyWhenNotOwner:       true,
			EmergencyAllowSmallCap: true,
			EmergencyCapPerWindow:  10,
		}
	}
	if policy.LocalCapPerWindow == 0 {
		policy.LocalCapPerWindow = 100
	}
	if policy.EmergencyCapPerWindow == 0 {
		policy.EmergencyCapPerWindow = 10
	}
	if !policy.DenyWhenNotOwner {
		policy.DenyWhenNotOwner = true
	}
	if !policy.EmergencyAllowSmallCap {
		policy.EmergencyAllowSmallCap = true
	}
	return policy
}
