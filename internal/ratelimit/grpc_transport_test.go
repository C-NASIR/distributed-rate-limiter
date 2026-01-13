package ratelimit

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	ratelimitv1 "ratelimit/internal/ratelimit/gen/ratelimit/v1"
)

const grpcBufSize = 1024 * 1024

func TestGRPC_CheckLimit_RuleNotFound(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport, conn := newGRPCTestServer(t, app, grpcTransportConfig{})
	defer closeGRPCTestServer(t, transport, conn)

	client := ratelimitv1.NewRateLimitServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := client.CheckLimit(ctx, &ratelimitv1.CheckLimitRequest{
		TenantId: "tenant",
		UserId:   "user",
		Resource: "resource",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("expected no rpc error: %v", err)
	}
	if resp.GetErrorCode() != "RULE_NOT_FOUND" {
		t.Fatalf("expected rule not found got %q", resp.GetErrorCode())
	}
}

func TestGRPC_AdminAuth_RejectsWithoutToken(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport, conn := newGRPCTestServer(t, app, grpcTransportConfig{
		enableAuth: true,
		adminToken: "token",
	})
	defer closeGRPCTestServer(t, transport, conn)

	client := ratelimitv1.NewAdminServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.CreateRule(ctx, &ratelimitv1.CreateRuleRequest{
		TenantId:  "tenant",
		Resource:  "resource",
		Algorithm: "token_bucket",
		Limit:     10,
		WindowMs:  int64(time.Second / time.Millisecond),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated error got %v", err)
	}
}

func TestGRPC_Admin_Create_Update_Conflict(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport, conn := newGRPCTestServer(t, app, grpcTransportConfig{})
	defer closeGRPCTestServer(t, transport, conn)

	client := ratelimitv1.NewAdminServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	rule, err := client.CreateRule(ctx, &ratelimitv1.CreateRuleRequest{
		TenantId:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		WindowMs:       int64(time.Second / time.Millisecond),
		IdempotencyKey: "grpc-1",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}
	if rule.GetVersion() != 1 {
		t.Fatalf("expected version 1 got %d", rule.GetVersion())
	}

	_, err = client.UpdateRule(ctx, &ratelimitv1.UpdateRuleRequest{
		TenantId:        "tenant",
		Resource:        "resource",
		Algorithm:       "token_bucket",
		Limit:           10,
		WindowMs:        int64(time.Second / time.Millisecond),
		ExpectedVersion: 2,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected conflict error got %v", err)
	}
}

func TestGRPC_CheckLimitBatch_PreservesOrder(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport, conn := newGRPCTestServer(t, app, grpcTransportConfig{})
	defer closeGRPCTestServer(t, transport, conn)

	_, err := app.AdminHandler.CreateRule(testContext(), &CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource-a",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "grpc-batch-1",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}
	_, err = app.AdminHandler.CreateRule(testContext(), &CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource-b",
		Algorithm:      "token_bucket",
		Limit:          5,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "grpc-batch-2",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}

	client := ratelimitv1.NewRateLimitServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := client.CheckLimitBatch(ctx, &ratelimitv1.BatchCheckLimitRequest{
		Requests: []*ratelimitv1.CheckLimitRequest{
			{TenantId: "tenant", UserId: "user", Resource: "resource-b", Cost: 1},
			{TenantId: "tenant", UserId: "user", Resource: "resource-a", Cost: 1},
			{TenantId: "tenant", UserId: "user", Resource: "missing", Cost: 1},
		},
	})
	if err != nil {
		t.Fatalf("failed to check batch: %v", err)
	}
	if len(resp.GetResponses()) != 3 {
		t.Fatalf("expected 3 responses got %d", len(resp.GetResponses()))
	}
	if resp.Responses[0].GetErrorCode() != "" || resp.Responses[1].GetErrorCode() != "" {
		t.Fatalf("expected allowed responses: %#v", resp.Responses)
	}
	if resp.Responses[2].GetErrorCode() != "RULE_NOT_FOUND" {
		t.Fatalf("expected rule not found got %q", resp.Responses[2].GetErrorCode())
	}
}

func TestGRPC_Health_Ready_Mode(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	ready := false
	transport, conn := newGRPCTestServerWithState(t, app, grpcTransportConfig{}, func() bool { return ready }, func() OperatingMode {
		return ModeDegraded
	})
	defer closeGRPCTestServer(t, transport, conn)

	client := ratelimitv1.NewHealthServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := client.Ready(ctx, &ratelimitv1.HealthRequest{})
	if err != nil {
		t.Fatalf("failed to call ready: %v", err)
	}
	if resp.GetStatus() != "not_ready" {
		t.Fatalf("expected not ready got %q", resp.GetStatus())
	}

	ready = true
	resp, err = client.Ready(ctx, &ratelimitv1.HealthRequest{})
	if err != nil {
		t.Fatalf("failed to call ready: %v", err)
	}
	if resp.GetStatus() != "ok" {
		t.Fatalf("expected ok got %q", resp.GetStatus())
	}

	resp, err = client.Mode(ctx, &ratelimitv1.HealthRequest{})
	if err != nil {
		t.Fatalf("failed to call mode: %v", err)
	}
	if resp.GetStatus() != "degraded" {
		t.Fatalf("expected degraded got %q", resp.GetStatus())
	}
}

func newGRPCTestServer(t *testing.T, app *Application, cfg grpcTransportConfig) (*GRPCTransport, *grpc.ClientConn) {
	t.Helper()
	return newGRPCTestServerWithState(t, app, cfg, func() bool { return true }, app.Mode)
}

func newGRPCTestServerWithState(t *testing.T, app *Application, cfg grpcTransportConfig, ready func() bool, mode func() OperatingMode) (*GRPCTransport, *grpc.ClientConn) {
	t.Helper()
	lis := bufconn.Listen(grpcBufSize)
	transport := NewGRPCTransport("bufnet", ready, mode, cfg)
	transport.lis = lis
	if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
		t.Fatalf("failed to register rate service: %v", err)
	}
	if err := transport.ServeAdmin(app.AdminHandler); err != nil {
		t.Fatalf("failed to register admin service: %v", err)
	}
	go func() {
		_ = transport.Start()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial grpc server: %v", err)
	}
	return transport, conn
}

func closeGRPCTestServer(t *testing.T, transport *GRPCTransport, conn *grpc.ClientConn) {
	t.Helper()
	if conn != nil {
		_ = conn.Close()
	}
	if transport == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := transport.Shutdown(ctx); err != nil {
		t.Fatalf("failed to shutdown grpc server: %v", err)
	}
}
