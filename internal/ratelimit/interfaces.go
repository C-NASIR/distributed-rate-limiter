// Package ratelimit defines service interfaces.
package ratelimit

import "context"

// RateLimitService evaluates rate limit requests.
type RateLimitService interface {
	CheckLimit(ctx context.Context, req *CheckLimitRequest) (*CheckLimitResponse, error)
	CheckLimitBatch(ctx context.Context, reqs []*CheckLimitRequest) ([]*CheckLimitResponse, error)
	ReleaseResponse(resp *CheckLimitResponse)
}

// AdminService manages rate limit rules.
type AdminService interface {
	CreateRule(ctx context.Context, req *CreateRuleRequest) (*Rule, error)
	UpdateRule(ctx context.Context, req *UpdateRuleRequest) (*Rule, error)
	DeleteRule(ctx context.Context, tenantID, resource string, expectedVersion int64) error
	GetRule(ctx context.Context, tenantID, resource string) (*Rule, error)
	ListRules(ctx context.Context, tenantID string) ([]*Rule, error)
}

// Transport exposes services over a transport layer.
type Transport interface {
	ServeRateLimit(service RateLimitService) error
	ServeAdmin(service AdminService) error
	Shutdown(ctx context.Context) error
}
