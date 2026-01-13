// Package ratelimit defines core request and rule models.
package ratelimit

import "time"

// CheckLimitRequest captures a single rate limit decision request.
type CheckLimitRequest struct {
	TraceID  string
	TenantID string
	UserID   string
	Resource string
	Cost     int64
}

// CheckLimitResponse reports the outcome of a rate limit decision.
type CheckLimitResponse struct {
	Allowed    bool
	Remaining  int64
	Limit      int64
	ResetAfter time.Duration
	RetryAfter time.Duration
	ErrorCode  string
}

// Rule describes a tenant rate limit configuration.
type Rule struct {
	TenantID  string
	Resource  string
	Algorithm string
	Limit     int64
	Window    time.Duration
	BurstSize int64
	Version   int64
	UpdatedAt time.Time
}

// CreateRuleRequest captures rule creation intent.
type CreateRuleRequest struct {
	TenantID       string
	Resource       string
	Algorithm      string
	Limit          int64
	Window         time.Duration
	BurstSize      int64
	IdempotencyKey string
}

// UpdateRuleRequest captures rule updates.
type UpdateRuleRequest struct {
	TenantID        string
	Resource        string
	Algorithm       string
	Limit           int64
	Window          time.Duration
	BurstSize       int64
	ExpectedVersion int64
}

// Decision captures the evaluated rate limit outcome.
type Decision struct {
	Allowed    bool
	Remaining  int64
	Limit      int64
	ResetAfter time.Duration
	RetryAfter time.Duration
}
