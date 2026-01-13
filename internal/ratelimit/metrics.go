// Package ratelimit provides in-memory metrics.
package ratelimit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// InMemoryMetrics stores counters and latency summaries.
type InMemoryMetrics struct {
	counters  sync.Map
	latencies sync.Map
}

type latencySummary struct {
	count      atomic.Int64
	totalNanos atomic.Int64
	maxNanos   atomic.Int64
}

// NewInMemoryMetrics constructs an in-memory metrics recorder.
func NewInMemoryMetrics() *InMemoryMetrics {
	return &InMemoryMetrics{}
}

// IncCheck increments a check counter.
func (m *InMemoryMetrics) IncCheck(result string, algorithm string, region string) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("check|%s|%s|%s", result, algorithm, region)
	m.incCounter(key)
}

// ObserveLatency tracks latency measurements.
func (m *InMemoryMetrics) ObserveLatency(op string, d time.Duration, region string) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("latency|%s|%s", op, region)
	entry := m.getLatency(key)
	if entry == nil {
		return
	}
	nanos := d.Nanoseconds()
	entry.count.Add(1)
	entry.totalNanos.Add(nanos)
	for {
		current := entry.maxNanos.Load()
		if nanos <= current {
			break
		}
		if entry.maxNanos.CompareAndSwap(current, nanos) {
			break
		}
	}
}

// IncFallback increments fallback counters.
func (m *InMemoryMetrics) IncFallback(reason string, region string) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("fallback|%s|%s", reason, region)
	m.incCounter(key)
}

// IncRedisError increments redis error counters.
func (m *InMemoryMetrics) IncRedisError(op string, region string) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("redis_error|%s|%s", op, region)
	m.incCounter(key)
}

// IncBatchItemError increments batch item error counters.
func (m *InMemoryMetrics) IncBatchItemError(code string, region string) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("batch_error|%s|%s", code, region)
	m.incCounter(key)
}

// Snapshot exports metrics values.
func (m *InMemoryMetrics) Snapshot() map[string]any {
	result := map[string]any{}
	if m == nil {
		return result
	}

	counters := map[string]int64{}
	m.counters.Range(func(key, value any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		counter, ok := value.(*atomic.Int64)
		if !ok || counter == nil {
			return true
		}
		counters[k] = counter.Load()
		return true
	})

	latencies := map[string]map[string]int64{}
	m.latencies.Range(func(key, value any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		entry, ok := value.(*latencySummary)
		if !ok || entry == nil {
			return true
		}
		latencies[k] = map[string]int64{
			"count":      entry.count.Load(),
			"totalNanos": entry.totalNanos.Load(),
			"maxNanos":   entry.maxNanos.Load(),
		}
		return true
	})

	result["counters"] = counters
	result["latencies"] = latencies
	return result
}

func (m *InMemoryMetrics) incCounter(key string) {
	counter := m.getCounter(key)
	if counter == nil {
		return
	}
	counter.Add(1)
}

func (m *InMemoryMetrics) getCounter(key string) *atomic.Int64 {
	if key == "" {
		return nil
	}
	if existing, ok := m.counters.Load(key); ok {
		if counter, ok := existing.(*atomic.Int64); ok {
			return counter
		}
	}
	counter := &atomic.Int64{}
	actual, _ := m.counters.LoadOrStore(key, counter)
	if stored, ok := actual.(*atomic.Int64); ok {
		return stored
	}
	return counter
}

func (m *InMemoryMetrics) getLatency(key string) *latencySummary {
	if key == "" {
		return nil
	}
	if existing, ok := m.latencies.Load(key); ok {
		if entry, ok := existing.(*latencySummary); ok {
			return entry
		}
	}
	entry := &latencySummary{}
	actual, _ := m.latencies.LoadOrStore(key, entry)
	if stored, ok := actual.(*latencySummary); ok {
		return stored
	}
	return entry
}
