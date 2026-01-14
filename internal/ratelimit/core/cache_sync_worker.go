// Package core provides cache synchronization workers.
package core

import (
	"context"
	"errors"
	"time"
)

// CacheSyncWorker periodically refreshes the rule cache.
type CacheSyncWorker struct {
	db       RuleDB
	rules    *RuleCache
	interval time.Duration
}

// NewCacheSyncWorker constructs a CacheSyncWorker.
func NewCacheSyncWorker(db RuleDB, rules *RuleCache, interval time.Duration) *CacheSyncWorker {
	return &CacheSyncWorker{
		db:       db,
		rules:    rules,
		interval: interval,
	}
}

// Start begins the synchronization loop.
func (w *CacheSyncWorker) Start(ctx context.Context) error {
	if w == nil || w.db == nil || w.rules == nil {
		return errors.New("cache sync worker is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := w.interval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			rules, err := w.db.LoadAll(ctx)
			if err != nil {
				continue
			}
			w.rules.ReplaceAll(rules)
		}
	}
}

// SetInterval overrides the sync interval.
func (w *CacheSyncWorker) SetInterval(interval time.Duration) {
	if w == nil {
		return
	}
	w.interval = interval
}
