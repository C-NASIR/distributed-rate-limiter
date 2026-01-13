package ratelimit

import (
	"context"
	"testing"
	"time"
)

// Benchmark note: best run with GOMAXPROCS set and go test -bench.

func BenchmarkCheckLimit_Allow(b *testing.B) {
	app := newBenchmarkApp(b)
	addBenchmarkRule(b, app)

	ctx := context.Background()
	request := &CheckLimitRequest{
		TenantID: "tenant",
		Resource: "resource",
		UserID:   "user",
		Cost:     1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := app.RateLimitHandler.CheckLimit(ctx, request)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		app.RateLimitHandler.ReleaseResponse(resp)
	}
}

func BenchmarkCheckLimitBatch_Allow(b *testing.B) {
	app := newBenchmarkApp(b)
	addBenchmarkRule(b, app)

	ctx := context.Background()
	const batchSize = 100
	requests := make([]*CheckLimitRequest, batchSize)
	for i := 0; i < batchSize; i++ {
		requests[i] = &CheckLimitRequest{
			TenantID: "tenant",
			Resource: "resource",
			UserID:   "user",
			Cost:     1,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		responses, err := app.RateLimitHandler.CheckLimitBatch(ctx, requests)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		for _, resp := range responses {
			app.RateLimitHandler.ReleaseResponse(resp)
		}
	}
}

func newBenchmarkApp(b *testing.B) *Application {
	b.Helper()
	app, err := NewApplication(&Config{Region: "bench"})
	if err != nil {
		b.Fatalf("failed to create app: %v", err)
	}
	return app
}

func addBenchmarkRule(b *testing.B, app *Application) {
	b.Helper()
	if app == nil || app.AdminHandler == nil {
		b.Fatalf("admin handler is required")
	}
	ctx := context.Background()
	_, err := app.AdminHandler.CreateRule(ctx, &CreateRuleRequest{
		TenantID:  "tenant",
		Resource:  "resource",
		Algorithm: "token_bucket",
		Limit:     1000,
		Window:    time.Second,
		BurstSize: 0,
	})
	if err != nil {
		b.Fatalf("failed to add rule: %v", err)
	}
}
