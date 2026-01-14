// Package core provides request coalescing.
package core

import (
	"context"
	"hash/fnv"
	"sync"
	"time"
)

// Coalescer deduplicates concurrent requests for identical keys.
type Coalescer struct {
	shards []coalescerShard
	ttl    time.Duration
}

type coalescerShard struct {
	mu sync.Mutex
	m  map[string]*coalesced
}

type coalesced struct {
	done     chan struct{}
	created  time.Time
	decision *Decision
	err      error
}

// NewCoalescer constructs a coalescer.
func NewCoalescer(shards int, ttl time.Duration) *Coalescer {
	if shards <= 0 {
		shards = 64
	}
	if ttl <= 0 {
		ttl = 10 * time.Millisecond
	}
	entries := make([]coalescerShard, shards)
	for i := range entries {
		entries[i] = coalescerShard{m: make(map[string]*coalesced)}
	}
	return &Coalescer{shards: entries, ttl: ttl}
}

// Do executes fn or waits for an inflight result.
func (c *Coalescer) Do(ctx context.Context, key string, fn func() (*Decision, error)) (*Decision, error) {
	if c == nil || len(c.shards) == 0 || c.ttl <= 0 {
		return fn()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	shard := c.shardFor(key)
	for {
		shard.mu.Lock()
		if existing, ok := shard.m[key]; ok {
			if time.Since(existing.created) <= c.ttl {
				done := existing.done
				shard.mu.Unlock()
				select {
				case <-done:
					return existing.decision, existing.err
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}

		entry := &coalesced{done: make(chan struct{}), created: time.Now()}
		shard.m[key] = entry
		shard.mu.Unlock()

		decision, err := fn()
		entry.decision = decision
		entry.err = err
		close(entry.done)

		shard.mu.Lock()
		if current, ok := shard.m[key]; ok && current == entry {
			delete(shard.m, key)
		}
		shard.mu.Unlock()
		return decision, err
	}
}

func (c *Coalescer) shardFor(key string) *coalescerShard {
	index := coalescerShardIndex(key, len(c.shards))
	return &c.shards[index]
}

func coalescerShardIndex(key string, total int) int {
	if total <= 1 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum32() % uint32(total))
}
