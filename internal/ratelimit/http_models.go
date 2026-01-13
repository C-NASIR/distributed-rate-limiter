// Package ratelimit provides HTTP transport models.
package ratelimit

import "time"

type httpCheckRequest struct {
	TraceID  string `json:"traceID"`
	TenantID string `json:"tenantID"`
	UserID   string `json:"userID"`
	Resource string `json:"resource"`
	Cost     int64  `json:"cost"`
}

type httpCreateRuleRequest struct {
	TenantID       string        `json:"tenantID"`
	Resource       string        `json:"resource"`
	Algorithm      string        `json:"algorithm"`
	Limit          int64         `json:"limit"`
	Window         time.Duration `json:"window"`
	BurstSize      int64         `json:"burstSize"`
	IdempotencyKey string        `json:"idempotencyKey"`
}

type httpUpdateRuleRequest struct {
	TenantID        string        `json:"tenantID"`
	Resource        string        `json:"resource"`
	Algorithm       string        `json:"algorithm"`
	Limit           int64         `json:"limit"`
	Window          time.Duration `json:"window"`
	BurstSize       int64         `json:"burstSize"`
	ExpectedVersion int64         `json:"expectedVersion"`
}

type httpCheckResponse struct {
	Allowed    bool          `json:"allowed"`
	Remaining  int64         `json:"remaining"`
	Limit      int64         `json:"limit"`
	ResetAfter time.Duration `json:"resetAfter"`
	RetryAfter time.Duration `json:"retryAfter"`
	ErrorCode  string        `json:"errorCode"`
}

type httpRuleResponse struct {
	TenantID  string        `json:"tenantID"`
	Resource  string        `json:"resource"`
	Algorithm string        `json:"algorithm"`
	Limit     int64         `json:"limit"`
	Window    time.Duration `json:"window"`
	BurstSize int64         `json:"burstSize"`
	Version   int64         `json:"version"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

func toCheckLimitRequest(req httpCheckRequest) *CheckLimitRequest {
	return &CheckLimitRequest{
		TraceID:  req.TraceID,
		TenantID: req.TenantID,
		UserID:   req.UserID,
		Resource: req.Resource,
		Cost:     req.Cost,
	}
}

func toCreateRuleRequest(req httpCreateRuleRequest) *CreateRuleRequest {
	return &CreateRuleRequest{
		TenantID:       req.TenantID,
		Resource:       req.Resource,
		Algorithm:      req.Algorithm,
		Limit:          req.Limit,
		Window:         req.Window,
		BurstSize:      req.BurstSize,
		IdempotencyKey: req.IdempotencyKey,
	}
}

func toUpdateRuleRequest(req httpUpdateRuleRequest) *UpdateRuleRequest {
	return &UpdateRuleRequest{
		TenantID:        req.TenantID,
		Resource:        req.Resource,
		Algorithm:       req.Algorithm,
		Limit:           req.Limit,
		Window:          req.Window,
		BurstSize:       req.BurstSize,
		ExpectedVersion: req.ExpectedVersion,
	}
}

func fromRule(rule *Rule) httpRuleResponse {
	if rule == nil {
		return httpRuleResponse{}
	}
	return httpRuleResponse{
		TenantID:  rule.TenantID,
		Resource:  rule.Resource,
		Algorithm: rule.Algorithm,
		Limit:     rule.Limit,
		Window:    rule.Window,
		BurstSize: rule.BurstSize,
		Version:   rule.Version,
		UpdatedAt: rule.UpdatedAt,
	}
}

func fromCheckLimitResponse(resp *CheckLimitResponse) httpCheckResponse {
	if resp == nil {
		return httpCheckResponse{}
	}
	return httpCheckResponse{
		Allowed:    resp.Allowed,
		Remaining:  resp.Remaining,
		Limit:      resp.Limit,
		ResetAfter: resp.ResetAfter,
		RetryAfter: resp.RetryAfter,
		ErrorCode:  resp.ErrorCode,
	}
}
