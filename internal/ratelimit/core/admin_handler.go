// Package core provides admin rule management.
package core

import (
	"context"
	"errors"
	"time"

	"ratelimit/internal/ratelimit/observability"
)

// OutboxWriter appends invalidation events.
type OutboxWriter interface {
	Insert(ctx context.Context, data []byte) (string, error)
}

// AdminHandler manages rule administration.
type AdminHandler struct {
	db           RuleDB
	rules        *RuleCache
	outboxWriter OutboxWriter
	tracer       observability.Tracer
	metrics      observability.Metrics
}

// NewAdminHandler constructs an AdminHandler.
func NewAdminHandler(db RuleDB, rules *RuleCache, outboxWriter OutboxWriter, tracer observability.Tracer, metrics observability.Metrics) *AdminHandler {
	return &AdminHandler{
		db:           db,
		rules:        rules,
		outboxWriter: outboxWriter,
		tracer:       tracer,
		metrics:      metrics,
	}
}

// CreateRule creates a new rule.
func (h *AdminHandler) CreateRule(ctx context.Context, req *CreateRuleRequest) (*Rule, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("rule db is not configured")
	}
	rule, err := h.db.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	if h.rules != nil {
		h.rules.UpsertIfNewer(rule)
	}
	if err := h.emitInvalidation(ctx, rule.TenantID, rule.Resource, "upsert", rule.Version); err != nil {
		return nil, err
	}
	return rule, nil
}

// UpdateRule updates an existing rule.
func (h *AdminHandler) UpdateRule(ctx context.Context, req *UpdateRuleRequest) (*Rule, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("rule db is not configured")
	}
	rule, err := h.db.Update(ctx, req)
	if err != nil {
		return nil, err
	}
	if h.rules != nil {
		h.rules.UpsertIfNewer(rule)
	}
	if err := h.emitInvalidation(ctx, rule.TenantID, rule.Resource, "upsert", rule.Version); err != nil {
		return nil, err
	}
	return rule, nil
}

// DeleteRule deletes a rule by tenant/resource.
func (h *AdminHandler) DeleteRule(ctx context.Context, tenantID, resource string, expectedVersion int64) error {
	if h == nil || h.db == nil {
		return errors.New("rule db is not configured")
	}
	if err := h.db.Delete(ctx, tenantID, resource, expectedVersion); err != nil {
		return err
	}
	if h.rules != nil {
		h.rules.DeleteIfOlderOrEqual(tenantID, resource, expectedVersion)
	}
	return h.emitInvalidation(ctx, tenantID, resource, "delete", expectedVersion)
}

// GetRule fetches a rule by tenant/resource.
func (h *AdminHandler) GetRule(ctx context.Context, tenantID, resource string) (*Rule, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("rule db is not configured")
	}
	return h.db.Get(ctx, tenantID, resource)
}

// ListRules lists rules for a tenant.
func (h *AdminHandler) ListRules(ctx context.Context, tenantID string) ([]*Rule, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("rule db is not configured")
	}
	return h.db.List(ctx, tenantID)
}

func (h *AdminHandler) emitInvalidation(ctx context.Context, tenantID, resource, action string, version int64) error {
	if h.outboxWriter == nil {
		return errors.New("outbox writer is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event := InvalidationEvent{
		TenantID:  tenantID,
		Resource:  resource,
		Action:    action,
		Version:   version,
		Timestamp: time.Now(),
	}
	data, err := MarshalInvalidationEvent(event)
	if err != nil {
		return err
	}
	_, err = h.outboxWriter.Insert(ctx, data)
	return err
}
