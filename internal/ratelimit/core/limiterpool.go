// Package core provides limiter pooling.
package core

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// LimiterPolicy configures limiter pooling.
type LimiterPolicy struct {
	Shards          int
	MaxEntriesShard int
	QuiesceWindow   time.Duration
	CloseTimeout    time.Duration
}

// LimiterPool caches limiters per tenant/resource.
type LimiterPool struct {
	shards  []LimiterPoolShard
	factory *LimiterFactory
	rules   *RuleCache
	policy  LimiterPolicy
}

// LimiterPoolShard stores shard-specific entries.
type LimiterPoolShard struct {
	mu  sync.Mutex
	m   map[string]*LimiterEntry
	lru *LRUKeys
}

// LimiterEntryState captures limiter lifecycle.
type LimiterEntryState int32

const (
	EntryActive LimiterEntryState = iota
	EntryQuiescing
	EntryClosed
)

// LimiterEntry stores limiter state.
type LimiterEntry struct {
	lim   Limiter
	state atomic.Int32
	refs  atomic.Int64
	mu    sync.Mutex
	cond  *sync.Cond
}

// LimiterHandle pins a limiter entry.
type LimiterHandle struct {
	entry *LimiterEntry
	lim   Limiter
	once  sync.Once
}

// Limiter returns the underlying limiter.
func (h *LimiterHandle) Limiter() Limiter {
	if h == nil {
		return nil
	}
	return h.lim
}

// Release releases a handle.
func (h *LimiterHandle) Release() {
	if h == nil || h.entry == nil {
		return
	}
	h.once.Do(func() {
		h.entry.release()
	})
}

// NewLimiterPool initializes a limiter pool.
func NewLimiterPool(rules *RuleCache, factory *LimiterFactory, policy LimiterPolicy) *LimiterPool {
	if policy.Shards <= 0 {
		policy.Shards = 16
	}
	if policy.MaxEntriesShard <= 0 {
		policy.MaxEntriesShard = 1024
	}
	if policy.QuiesceWindow <= 0 {
		policy.QuiesceWindow = 50 * time.Millisecond
	}
	if policy.CloseTimeout <= 0 {
		policy.CloseTimeout = 2 * time.Second
	}

	shards := make([]LimiterPoolShard, policy.Shards)
	for i := range shards {
		shards[i] = LimiterPoolShard{
			m:   make(map[string]*LimiterEntry),
			lru: NewLRUKeys(policy.MaxEntriesShard),
		}
	}

	return &LimiterPool{
		shards:  shards,
		factory: factory,
		rules:   rules,
		policy:  policy,
	}
}

// Acquire returns a limiter handle for a tenant/resource.
func (pool *LimiterPool) Acquire(ctx context.Context, tenantID, resource string) (*LimiterHandle, *Rule, error) {
	if pool == nil || pool.rules == nil {
		return nil, nil, errors.New("limiter pool is not initialized")
	}
	rule, ok := pool.rules.Get(tenantID, resource)
	if !ok || rule == nil {
		return nil, nil, errors.New("rule not found")
	}
	shard := pool.shardFor(tenantID, resource)
	key := limiterPoolKey(tenantID, resource)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if entry, ok := shard.m[key]; ok && entry != nil {
		state := LimiterEntryState(entry.state.Load())
		switch state {
		case EntryActive:
			entry.refs.Add(1)
			shard.lru.Touch(key)
			return &LimiterHandle{entry: entry, lim: entry.lim}, rule, nil
		case EntryQuiescing, EntryClosed:
			// Treat as missing.
		}
	}

	limiter, _, err := pool.factory.Create(rule)
	if err != nil {
		return nil, nil, err
	}
	entry := newLimiterEntry(limiter)
	entry.state.Store(int32(EntryActive))
	entry.refs.Store(1)

	shard.m[key] = entry
	shard.lru.Add(key)

	pool.evictIfNeededLocked(shard)

	return &LimiterHandle{entry: entry, lim: limiter}, rule, nil
}

// Cutover transitions an entry to a new limiter.
func (pool *LimiterPool) Cutover(ctx context.Context, tenantID, resource string, newRuleVersion int64) {
	if pool == nil {
		return
	}
	shard := pool.shardFor(tenantID, resource)
	key := limiterPoolKey(tenantID, resource)

	var entry *LimiterEntry
	shard.mu.Lock()
	entry = shard.m[key]
	if entry == nil {
		shard.mu.Unlock()
		return
	}
	if LimiterEntryState(entry.state.Load()) != EntryQuiescing {
		entry.state.Store(int32(EntryQuiescing))
	}
	delete(shard.m, key)
	shard.lru.Remove(key)
	shard.mu.Unlock()

	select {
	case <-time.After(pool.policy.QuiesceWindow):
	case <-ctx.Done():
	}

	pool.waitForEntry(entry, pool.policy.CloseTimeout)
	entry.closeLimiter()
}

func (pool *LimiterPool) shardFor(tenantID, resource string) *LimiterPoolShard {
	index := shardIndex(tenantID, resource, len(pool.shards))
	return &pool.shards[index]
}

func shardIndex(tenantID, resource string, total int) int {
	if total <= 1 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(tenantID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(resource))
	return int(hasher.Sum32() % uint32(total))
}

func newLimiterEntry(limiter Limiter) *LimiterEntry {
	entry := &LimiterEntry{lim: limiter}
	entry.cond = sync.NewCond(&entry.mu)
	return entry
}

func (pool *LimiterPool) evictIfNeededLocked(shard *LimiterPoolShard) {
	if shard == nil {
		return
	}
	evicted := shard.lru.EvictIfNeeded()
	if len(evicted) == 0 {
		return
	}
	for _, key := range evicted {
		entry := shard.m[key]
		if entry == nil {
			continue
		}
		delete(shard.m, key)
		if entry.refs.Load() == 0 {
			entry.closeLimiter()
			continue
		}
		entry.state.Store(int32(EntryQuiescing))
		go pool.waitAndClose(entry)
	}
}

func (pool *LimiterPool) waitAndClose(entry *LimiterEntry) {
	if entry == nil {
		return
	}
	pool.waitForEntry(entry, pool.policy.CloseTimeout)
	entry.closeLimiter()
}

func (pool *LimiterPool) waitForEntry(entry *LimiterEntry, timeout time.Duration) {
	if entry == nil {
		return
	}
	deadline := time.Now().Add(timeout)
	entry.mu.Lock()
	if entry.cond == nil {
		entry.cond = sync.NewCond(&entry.mu)
	}
	timer := time.AfterFunc(timeout, func() {
		entry.mu.Lock()
		entry.cond.Broadcast()
		entry.mu.Unlock()
	})
	for entry.refs.Load() > 0 {
		if time.Now().After(deadline) {
			break
		}
		entry.cond.Wait()
	}
	entry.mu.Unlock()
	if !timer.Stop() {
		return
	}
}

func (entry *LimiterEntry) release() {
	if entry == nil {
		return
	}
	refs := entry.refs.Add(-1)
	if refs < 0 {
		entry.refs.Store(0)
		refs = 0
	}
	if refs == 0 && LimiterEntryState(entry.state.Load()) == EntryQuiescing {
		entry.mu.Lock()
		if entry.cond != nil {
			entry.cond.Broadcast()
		}
		entry.mu.Unlock()
	}
}

func (entry *LimiterEntry) closeLimiter() {
	if entry == nil {
		return
	}
	if LimiterEntryState(entry.state.Swap(int32(EntryClosed))) == EntryClosed {
		return
	}
	_ = entry.lim.Close()
}
