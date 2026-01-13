// Package ratelimit defines admin interfaces and event models.
package ratelimit

import "context"

// RuleDB stores rate limit rules.
type RuleDB interface {
	Create(ctx context.Context, req *CreateRuleRequest) (*Rule, error)
	Update(ctx context.Context, req *UpdateRuleRequest) (*Rule, error)
	Delete(ctx context.Context, tenantID, resource string, expectedVersion int64) error
	Get(ctx context.Context, tenantID, resource string) (*Rule, error)
	List(ctx context.Context, tenantID string) ([]*Rule, error)
	LoadAll(ctx context.Context) ([]*Rule, error)
}

// OutboxRow stores an outbox payload.
type OutboxRow struct {
	ID   string
	Data []byte
}

// Outbox stores invalidation events for publishing.
type Outbox interface {
	FetchPending(ctx context.Context, limit int) ([]OutboxRow, error)
	MarkSent(ctx context.Context, id string) error
}

// PubSub delivers invalidation events to subscribers.
type PubSub interface {
	Subscribe(ctx context.Context, channel string, handler func(context.Context, []byte)) error
	Publish(ctx context.Context, channel string, payload []byte) error
}

// Tracer is an optional tracing dependency.
type Tracer interface{}

// Sampler is an optional tracing sampler.
type Sampler interface{}

// Metrics is an optional metrics dependency.
type Metrics interface{}
