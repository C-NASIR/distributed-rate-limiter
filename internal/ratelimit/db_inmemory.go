// Package ratelimit provides in-memory rule storage.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// InMemoryRuleDB stores rules in memory.
type InMemoryRuleDB struct {
	mu          sync.Mutex
	rules       map[string]*Rule
	idempotency map[string]idempotencyRecord
	now         func() time.Time
}

type idempotencyRecord struct {
	ruleKey     string
	payloadHash string
}

// NewInMemoryRuleDB constructs an in-memory rule database.
func NewInMemoryRuleDB(now func() time.Time) *InMemoryRuleDB {
	if now == nil {
		now = time.Now
	}
	return &InMemoryRuleDB{
		rules:       make(map[string]*Rule),
		idempotency: make(map[string]idempotencyRecord),
		now:         now,
	}
}

// Create inserts a rule with idempotency enforcement.
func (db *InMemoryRuleDB) Create(ctx context.Context, req *CreateRuleRequest) (*Rule, error) {
	if req == nil {
		return nil, ErrInvalidInput
	}
	if req.TenantID == "" || req.Resource == "" {
		return nil, ErrInvalidInput
	}
	if req.Limit <= 0 {
		return nil, ErrInvalidInput
	}
	key := ruleKey(req.TenantID, req.Resource)
	payloadHash := hashCreatePayload(req)

	db.mu.Lock()
	defer db.mu.Unlock()

	if req.IdempotencyKey != "" {
		if record, ok := db.idempotency[req.IdempotencyKey]; ok {
			if record.payloadHash != payloadHash {
				return nil, ErrConflict
			}
			rule := db.rules[record.ruleKey]
			if rule == nil {
				return nil, ErrConflict
			}
			return cloneRule(rule), nil
		}
	}

	if _, ok := db.rules[key]; ok {
		return nil, ErrConflict
	}

	rule := &Rule{
		TenantID:  req.TenantID,
		Resource:  req.Resource,
		Algorithm: req.Algorithm,
		Limit:     req.Limit,
		Window:    req.Window,
		BurstSize: req.BurstSize,
		Version:   1,
		UpdatedAt: db.now(),
	}

	db.rules[key] = rule
	if req.IdempotencyKey != "" {
		db.idempotency[req.IdempotencyKey] = idempotencyRecord{ruleKey: key, payloadHash: payloadHash}
	}

	return cloneRule(rule), nil
}

// Update modifies a rule with optimistic concurrency control.
func (db *InMemoryRuleDB) Update(ctx context.Context, req *UpdateRuleRequest) (*Rule, error) {
	if req == nil {
		return nil, ErrInvalidInput
	}
	if req.TenantID == "" || req.Resource == "" {
		return nil, ErrInvalidInput
	}
	if req.Limit <= 0 {
		return nil, ErrInvalidInput
	}
	key := ruleKey(req.TenantID, req.Resource)

	db.mu.Lock()
	defer db.mu.Unlock()

	existing := db.rules[key]
	if existing == nil {
		return nil, ErrNotFound
	}
	if existing.Version != req.ExpectedVersion {
		return nil, ErrConflict
	}

	rule := &Rule{
		TenantID:  req.TenantID,
		Resource:  req.Resource,
		Algorithm: req.Algorithm,
		Limit:     req.Limit,
		Window:    req.Window,
		BurstSize: req.BurstSize,
		Version:   existing.Version + 1,
		UpdatedAt: db.now(),
	}

	db.rules[key] = rule
	return cloneRule(rule), nil
}

// Delete removes a rule if the version matches.
func (db *InMemoryRuleDB) Delete(ctx context.Context, tenantID, resource string, expectedVersion int64) error {
	if tenantID == "" || resource == "" {
		return ErrInvalidInput
	}
	key := ruleKey(tenantID, resource)

	db.mu.Lock()
	defer db.mu.Unlock()

	rule := db.rules[key]
	if rule == nil {
		return ErrNotFound
	}
	if rule.Version != expectedVersion {
		return ErrConflict
	}
	delete(db.rules, key)
	return nil
}

// Get returns a rule by tenant/resource.
func (db *InMemoryRuleDB) Get(ctx context.Context, tenantID, resource string) (*Rule, error) {
	if tenantID == "" || resource == "" {
		return nil, ErrInvalidInput
	}
	key := ruleKey(tenantID, resource)

	db.mu.Lock()
	defer db.mu.Unlock()

	rule := db.rules[key]
	if rule == nil {
		return nil, ErrNotFound
	}
	return cloneRule(rule), nil
}

// List returns all rules for a tenant.
func (db *InMemoryRuleDB) List(ctx context.Context, tenantID string) ([]*Rule, error) {
	if tenantID == "" {
		return nil, ErrInvalidInput
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	var rules []*Rule
	for _, rule := range db.rules {
		if rule == nil || rule.TenantID != tenantID {
			continue
		}
		rules = append(rules, cloneRule(rule))
	}
	if rules == nil {
		return []*Rule{}, nil
	}
	return rules, nil
}

// LoadAll returns all rules.
func (db *InMemoryRuleDB) LoadAll(ctx context.Context) ([]*Rule, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if len(db.rules) == 0 {
		return []*Rule{}, nil
	}
	rules := make([]*Rule, 0, len(db.rules))
	for _, rule := range db.rules {
		rules = append(rules, cloneRule(rule))
	}
	return rules, nil
}

func ruleKey(tenantID, resource string) string {
	return fmt.Sprintf("%s\x1f%s", tenantID, resource)
}

func hashCreatePayload(req *CreateRuleRequest) string {
	if req == nil {
		return ""
	}
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "%s\x1f%s\x1f%s\x1f%d\x1f%d\x1f%d", req.TenantID, req.Resource, req.Algorithm, req.Limit, req.Window, req.BurstSize)
	return hex.EncodeToString(hasher.Sum(nil))
}
