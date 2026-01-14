// Package observability provides tracing helpers.
package observability

import (
	"context"
	"hash/fnv"
)

// NoopTracer is a tracer that records nothing.
type NoopTracer struct{}

// NoopSpan is a span that records nothing.
type NoopSpan struct{}

// StartSpan starts a span that does nothing.
func (t NoopTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return ctx, NoopSpan{}
}

// SetAttribute is a no-op.
func (s NoopSpan) SetAttribute(key, value string) {}

// RecordError is a no-op.
func (s NoopSpan) RecordError(err error) {}

// End is a no-op.
func (s NoopSpan) End() {}

// HashSampler samples traces by hashing the trace ID.
type HashSampler struct {
	rate int
}

// NewHashSampler returns a HashSampler with the provided rate.
func NewHashSampler(rate int) HashSampler {
	return HashSampler{rate: rate}
}

// Sampled reports whether the trace should be sampled.
func (s HashSampler) Sampled(traceID string) bool {
	if traceID == "" {
		return false
	}
	rate := s.rate
	if rate <= 0 {
		return false
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(traceID))
	return int(hasher.Sum32()%uint32(rate)) == 0
}
