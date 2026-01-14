// Package observability defines tracing and metrics interfaces.
package observability

import (
	"context"
	"time"
)

// Span captures tracing span operations.
type Span interface {
	SetAttribute(key, value string)
	RecordError(err error)
	End()
}

// Tracer is an optional tracing dependency.
type Tracer interface {
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Sampler decides if a trace should be sampled.
type Sampler interface {
	Sampled(traceID string) bool
}

// Metrics records service measurements.
type Metrics interface {
	IncCheck(result string, algorithm string, region string)
	ObserveLatency(op string, d time.Duration, region string)
	IncFallback(reason string, region string)
	IncRedisError(op string, region string)
	IncBatchItemError(code string, region string)
}
