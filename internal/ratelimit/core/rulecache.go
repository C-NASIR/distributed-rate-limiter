// Package core provides rate limit rule caching.
package core

import (
	"sync"
	"sync/atomic"
)

// TenantRulesSnapshot stores rules per resource for a tenant.
type TenantRulesSnapshot struct {
	byResource map[string]*Rule
}

// GlobalRulesSnapshot stores rules per tenant.
type GlobalRulesSnapshot struct {
	byTenant map[string]*TenantRulesSnapshot
}

// RuleCache stores rule snapshots with copy-on-write updates.
type RuleCache struct {
	snap atomic.Value
	mu   sync.Mutex
}

// NewRuleCache creates a new cache with an empty snapshot.
func NewRuleCache() *RuleCache {
	cache := &RuleCache{}
	cache.snap.Store(&GlobalRulesSnapshot{byTenant: map[string]*TenantRulesSnapshot{}})
	return cache
}

// Get returns a rule for the given tenant/resource.
func (rc *RuleCache) Get(tenantID, resource string) (*Rule, bool) {
	snapshot := rc.snapshot()
	tenantRules, ok := snapshot.byTenant[tenantID]
	if !ok || tenantRules == nil {
		return nil, false
	}
	rule, ok := tenantRules.byResource[resource]
	return rule, ok
}

// List returns all rules for the tenant.
func (rc *RuleCache) List(tenantID string) []*Rule {
	snapshot := rc.snapshot()
	tenantRules, ok := snapshot.byTenant[tenantID]
	if !ok || tenantRules == nil {
		return nil
	}
	if len(tenantRules.byResource) == 0 {
		return []*Rule{}
	}
	rules := make([]*Rule, 0, len(tenantRules.byResource))
	for _, rule := range tenantRules.byResource {
		rules = append(rules, rule)
	}
	return rules
}

// ReplaceAll replaces the entire snapshot with the provided rules.
func (rc *RuleCache) ReplaceAll(rules []*Rule) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	tenantCounts := make(map[string]int)
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		tenantCounts[rule.TenantID]++
	}

	byTenant := make(map[string]*TenantRulesSnapshot, len(tenantCounts))
	for tenantID, count := range tenantCounts {
		byTenant[tenantID] = &TenantRulesSnapshot{byResource: make(map[string]*Rule, count)}
	}

	for _, rule := range rules {
		if rule == nil {
			continue
		}
		clone := cloneRule(rule)
		tenantRules := byTenant[clone.TenantID]
		tenantRules.byResource[clone.Resource] = clone
	}

	rc.snap.Store(&GlobalRulesSnapshot{byTenant: byTenant})
}

// UpsertIfNewer updates a rule if it has a newer version.
func (rc *RuleCache) UpsertIfNewer(rule *Rule) {
	if rule == nil {
		return
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()

	snapshot := rc.snapshot()
	if tenantRules, ok := snapshot.byTenant[rule.TenantID]; ok && tenantRules != nil {
		if existing, ok := tenantRules.byResource[rule.Resource]; ok && existing != nil {
			if rule.Version <= existing.Version {
				return
			}
		}
	}

	byTenant := copyTenantMap(snapshot.byTenant)
	oldTenant := snapshot.byTenant[rule.TenantID]
	var byResource map[string]*Rule
	if oldTenant == nil {
		byResource = make(map[string]*Rule, 1)
	} else {
		byResource = copyResourceMap(oldTenant.byResource)
	}
	byResource[rule.Resource] = cloneRule(rule)
	byTenant[rule.TenantID] = &TenantRulesSnapshot{byResource: byResource}

	rc.snap.Store(&GlobalRulesSnapshot{byTenant: byTenant})
}

// DeleteIfOlderOrEqual removes a rule if its version is older or equal.
func (rc *RuleCache) DeleteIfOlderOrEqual(tenantID, resource string, version int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	snapshot := rc.snapshot()
	oldTenant, ok := snapshot.byTenant[tenantID]
	if !ok || oldTenant == nil {
		return
	}
	existing, ok := oldTenant.byResource[resource]
	if !ok || existing == nil {
		return
	}
	if existing.Version > version {
		return
	}

	byTenant := copyTenantMap(snapshot.byTenant)
	byResource := copyResourceMap(oldTenant.byResource)
	delete(byResource, resource)
	if len(byResource) == 0 {
		delete(byTenant, tenantID)
	} else {
		byTenant[tenantID] = &TenantRulesSnapshot{byResource: byResource}
	}

	rc.snap.Store(&GlobalRulesSnapshot{byTenant: byTenant})
}

func (rc *RuleCache) snapshot() *GlobalRulesSnapshot {
	if snapshot, ok := rc.snap.Load().(*GlobalRulesSnapshot); ok && snapshot != nil {
		return snapshot
	}
	return &GlobalRulesSnapshot{byTenant: map[string]*TenantRulesSnapshot{}}
}

func cloneRule(rule *Rule) *Rule {
	if rule == nil {
		return nil
	}
	clone := *rule
	return &clone
}

func copyTenantMap(old map[string]*TenantRulesSnapshot) map[string]*TenantRulesSnapshot {
	if old == nil {
		return map[string]*TenantRulesSnapshot{}
	}
	copyMap := make(map[string]*TenantRulesSnapshot, len(old))
	for key, value := range old {
		copyMap[key] = value
	}
	return copyMap
}

func copyResourceMap(old map[string]*Rule) map[string]*Rule {
	if old == nil {
		return map[string]*Rule{}
	}
	copyMap := make(map[string]*Rule, len(old))
	for key, value := range old {
		copyMap[key] = value
	}
	return copyMap
}
