// Package ratelimit provides gRPC interceptors.
package ratelimit

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	ratelimitv1 "ratelimit/internal/ratelimit/gen/ratelimit/v1"
)

func grpcRequestIDInterceptor(logger Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		requestID := strconv.FormatInt(time.Now().UnixNano(), 10)
		start := time.Now()
		resp, err := handler(ctx, req)
		if logger != nil {
			fields := map[string]any{
				"method":      info.FullMethod,
				"request_id":  requestID,
				"duration_ms": time.Since(start).Milliseconds(),
			}
			if err != nil {
				fields["error"] = err.Error()
				logger.Error("grpc request error", fields)
			} else {
				logger.Info("grpc request", fields)
			}
		}
		return resp, err
	}
}

func grpcAuthInterceptor(enableAuth bool, adminToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !enableAuth {
			return handler(ctx, req)
		}
		if !strings.HasPrefix(info.FullMethod, "/ratelimit.v1.AdminService/") {
			return handler(ctx, req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		expected := "Bearer " + adminToken
		values := md.Get("authorization")
		if len(values) == 0 || values[0] != expected {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		return handler(ctx, req)
	}
}

func grpcTracingMetricsInterceptor(tracer Tracer, sampler Sampler, metrics Metrics, region string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		method := grpcMethodName(info.FullMethod)
		traceID := grpcTraceID(req)
		span := Span(nil)
		if tracer != nil && sampler != nil && sampler.Sampled(traceID) {
			ctx, span = tracer.StartSpan(ctx, method)
			span.SetAttribute("method", method)
		}
		start := time.Now()
		resp, err := handler(ctx, req)
		if span != nil {
			if err != nil {
				span.RecordError(err)
			}
			span.End()
		}
		if metrics != nil {
			metrics.ObserveLatency(method, time.Since(start), regionLabel(region))
		}
		recordGRPCResult(metrics, method, err)
		return resp, err
	}
}

func regionLabel(region string) string {
	if region == "" {
		return "unknown"
	}
	return region
}

func grpcMethodName(fullMethod string) string {
	if fullMethod == "" {
		return "unknown"
	}
	return path.Base(fullMethod)
}

func grpcTraceID(req any) string {
	switch value := req.(type) {
	case interface{ GetTraceId() string }:
		return value.GetTraceId()
	case *ratelimitv1.BatchCheckLimitRequest:
		for _, item := range value.GetRequests() {
			if item != nil && item.GetTraceId() != "" {
				return item.GetTraceId()
			}
		}
	}
	return ""
}

func recordGRPCResult(metrics Metrics, method string, err error) {
	if metrics == nil || method == "" {
		return
	}
	mem, ok := metrics.(*InMemoryMetrics)
	if !ok || mem == nil {
		return
	}
	result := "success"
	if err != nil {
		result = strings.ToLower(status.Code(err).String())
	}
	mem.incCounter(fmt.Sprintf("grpc|%s|%s", method, result))
}
