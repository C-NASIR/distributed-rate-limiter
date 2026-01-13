// Package ratelimit provides a gRPC transport.
package ratelimit

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	ratelimitv1 "ratelimit/internal/ratelimit/gen/ratelimit/v1"
)

// GRPCTransport serves the RateLimit and Admin APIs over gRPC.
type GRPCTransport struct {
	addr  string
	lis   net.Listener
	srv   *grpc.Server
	rate  RateLimitService
	admin AdminService
	ready func() bool
	mode  func() OperatingMode
	cfg   grpcTransportConfig
	mu    sync.Mutex
}

type grpcTransportConfig struct {
	enableAuth bool
	adminToken string
	keepAlive  time.Duration
	tracer     Tracer
	sampler    Sampler
	metrics    Metrics
	logger     Logger
}

// NewGRPCTransport constructs a transport bound to an address.
func NewGRPCTransport(addr string, ready func() bool, mode func() OperatingMode, cfg grpcTransportConfig) *GRPCTransport {
	if addr == "" {
		addr = ":9090"
	}
	if ready == nil {
		ready = func() bool { return false }
	}
	if mode == nil {
		mode = func() OperatingMode { return ModeNormal }
	}
	if cfg.keepAlive <= 0 {
		cfg.keepAlive = 60 * time.Second
	}
	return &GRPCTransport{addr: addr, ready: ready, mode: mode, cfg: cfg}
}

// ServeRateLimit registers the rate limit service.
func (t *GRPCTransport) ServeRateLimit(service RateLimitService) error {
	if service == nil {
		return errors.New("rate limit service is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate = service
	return nil
}

// ServeAdmin registers the admin service.
func (t *GRPCTransport) ServeAdmin(service AdminService) error {
	if service == nil {
		return errors.New("admin service is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.admin = service
	return nil
}

// Start begins serving gRPC requests.
func (t *GRPCTransport) Start() error {
	if t == nil {
		return errors.New("grpc transport is nil")
	}
	t.mu.Lock()
	if t.rate == nil || t.admin == nil {
		t.mu.Unlock()
		return errors.New("services must be registered before starting")
	}
	listener := t.lis
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", t.addr)
		if err != nil {
			t.mu.Unlock()
			return err
		}
		t.lis = listener
	}
	if t.srv == nil {
		opts := []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				grpcRequestIDInterceptor(t.cfg.logger),
				grpcAuthInterceptor(t.cfg.enableAuth, t.cfg.adminToken),
				grpcTracingMetricsInterceptor(t.cfg.tracer, t.cfg.sampler, t.cfg.metrics),
			),
			grpc.KeepaliveParams(keepalive.ServerParameters{Time: t.cfg.keepAlive}),
		}
		t.srv = grpc.NewServer(opts...)
		ratelimitv1.RegisterRateLimitServiceServer(t.srv, &grpcRateLimitServer{service: t.rate})
		ratelimitv1.RegisterAdminServiceServer(t.srv, &grpcAdminServer{service: t.admin})
		ratelimitv1.RegisterHealthServiceServer(t.srv, &grpcHealthServer{ready: t.ready, mode: t.mode})
	}
	srv := t.srv
	t.mu.Unlock()

	if err := srv.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Shutdown stops the gRPC server.
func (t *GRPCTransport) Shutdown(ctx context.Context) error {
	if t == nil {
		return errors.New("grpc transport is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	srv := t.srv
	listener := t.lis
	t.mu.Unlock()
	if srv == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		srv.Stop()
		if listener != nil {
			_ = listener.Close()
		}
		return ctx.Err()
	}
	if listener != nil {
		_ = listener.Close()
	}
	return nil
}

type grpcRateLimitServer struct {
	ratelimitv1.UnimplementedRateLimitServiceServer
	service RateLimitService
}

func (s *grpcRateLimitServer) CheckLimit(ctx context.Context, req *ratelimitv1.CheckLimitRequest) (*ratelimitv1.CheckLimitResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "rate limit service is required")
	}
	resp, err := s.service.CheckLimit(ctx, &CheckLimitRequest{
		TraceID:  req.GetTraceId(),
		TenantID: req.GetTenantId(),
		UserID:   req.GetUserId(),
		Resource: req.GetResource(),
		Cost:     req.GetCost(),
	})
	if err != nil {
		code := CodeOf(err)
		if code == CodeInvalidInput || code == CodeInvalidCost {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return &ratelimitv1.CheckLimitResponse{
			Allowed:   false,
			ErrorCode: grpcErrorCode(code),
		}, nil
	}
	return toGRPCCheckLimitResponse(resp), nil
}

func (s *grpcRateLimitServer) CheckLimitBatch(ctx context.Context, req *ratelimitv1.BatchCheckLimitRequest) (*ratelimitv1.BatchCheckLimitResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "rate limit service is required")
	}
	requests := make([]*CheckLimitRequest, len(req.GetRequests()))
	for i, item := range req.GetRequests() {
		if item == nil {
			continue
		}
		requests[i] = &CheckLimitRequest{
			TraceID:  item.GetTraceId(),
			TenantID: item.GetTenantId(),
			UserID:   item.GetUserId(),
			Resource: item.GetResource(),
			Cost:     item.GetCost(),
		}
	}
	responses, err := s.service.CheckLimitBatch(ctx, requests)
	if err != nil {
		code := CodeOf(err)
		if code == CodeInvalidInput || code == CodeInvalidCost {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	result := make([]*ratelimitv1.CheckLimitResponse, len(responses))
	for i, resp := range responses {
		result[i] = toGRPCCheckLimitResponse(resp)
	}
	return &ratelimitv1.BatchCheckLimitResponse{Responses: result}, nil
}

type grpcAdminServer struct {
	ratelimitv1.UnimplementedAdminServiceServer
	service AdminService
}

func (s *grpcAdminServer) CreateRule(ctx context.Context, req *ratelimitv1.CreateRuleRequest) (*ratelimitv1.Rule, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "admin service is required")
	}
	rule, err := s.service.CreateRule(ctx, &CreateRuleRequest{
		TenantID:       req.GetTenantId(),
		Resource:       req.GetResource(),
		Algorithm:      req.GetAlgorithm(),
		Limit:          req.GetLimit(),
		Window:         time.Duration(req.GetWindowMs()) * time.Millisecond,
		BurstSize:      req.GetBurstSize(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, grpcError(err)
	}
	return toGRPCRule(rule), nil
}

func (s *grpcAdminServer) UpdateRule(ctx context.Context, req *ratelimitv1.UpdateRuleRequest) (*ratelimitv1.Rule, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "admin service is required")
	}
	rule, err := s.service.UpdateRule(ctx, &UpdateRuleRequest{
		TenantID:        req.GetTenantId(),
		Resource:        req.GetResource(),
		Algorithm:       req.GetAlgorithm(),
		Limit:           req.GetLimit(),
		Window:          time.Duration(req.GetWindowMs()) * time.Millisecond,
		BurstSize:       req.GetBurstSize(),
		ExpectedVersion: req.GetExpectedVersion(),
	})
	if err != nil {
		return nil, grpcError(err)
	}
	return toGRPCRule(rule), nil
}

func (s *grpcAdminServer) DeleteRule(ctx context.Context, req *ratelimitv1.DeleteRuleRequest) (*ratelimitv1.HealthResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "admin service is required")
	}
	if err := s.service.DeleteRule(ctx, req.GetTenantId(), req.GetResource(), req.GetExpectedVersion()); err != nil {
		return nil, grpcError(err)
	}
	return &ratelimitv1.HealthResponse{Status: "ok"}, nil
}

func (s *grpcAdminServer) GetRule(ctx context.Context, req *ratelimitv1.GetRuleRequest) (*ratelimitv1.Rule, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "admin service is required")
	}
	rule, err := s.service.GetRule(ctx, req.GetTenantId(), req.GetResource())
	if err != nil {
		return nil, grpcError(err)
	}
	return toGRPCRule(rule), nil
}

func (s *grpcAdminServer) ListRules(ctx context.Context, req *ratelimitv1.ListRulesRequest) (*ratelimitv1.ListRulesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "admin service is required")
	}
	rules, err := s.service.ListRules(ctx, req.GetTenantId())
	if err != nil {
		return nil, grpcError(err)
	}
	resp := make([]*ratelimitv1.Rule, len(rules))
	for i, rule := range rules {
		resp[i] = toGRPCRule(rule)
	}
	return &ratelimitv1.ListRulesResponse{Rules: resp}, nil
}

type grpcHealthServer struct {
	ratelimitv1.UnimplementedHealthServiceServer
	ready func() bool
	mode  func() OperatingMode
}

func (s *grpcHealthServer) Health(context.Context, *ratelimitv1.HealthRequest) (*ratelimitv1.HealthResponse, error) {
	return &ratelimitv1.HealthResponse{Status: "ok"}, nil
}

func (s *grpcHealthServer) Ready(context.Context, *ratelimitv1.HealthRequest) (*ratelimitv1.HealthResponse, error) {
	if s != nil && s.ready != nil && s.ready() {
		return &ratelimitv1.HealthResponse{Status: "ok"}, nil
	}
	return &ratelimitv1.HealthResponse{Status: "not_ready"}, nil
}

func (s *grpcHealthServer) Mode(context.Context, *ratelimitv1.HealthRequest) (*ratelimitv1.HealthResponse, error) {
	mode := ModeNormal
	if s != nil && s.mode != nil {
		mode = s.mode()
	}
	return &ratelimitv1.HealthResponse{Status: modeLabel(mode)}, nil
}

func toGRPCCheckLimitResponse(resp *CheckLimitResponse) *ratelimitv1.CheckLimitResponse {
	if resp == nil {
		return &ratelimitv1.CheckLimitResponse{}
	}
	return &ratelimitv1.CheckLimitResponse{
		Allowed:      resp.Allowed,
		Remaining:    resp.Remaining,
		Limit:        resp.Limit,
		ResetAfterMs: resp.ResetAfter.Milliseconds(),
		RetryAfterMs: resp.RetryAfter.Milliseconds(),
		ErrorCode:    resp.ErrorCode,
	}
}

func toGRPCRule(rule *Rule) *ratelimitv1.Rule {
	if rule == nil {
		return &ratelimitv1.Rule{}
	}
	updatedAt := int64(0)
	if !rule.UpdatedAt.IsZero() {
		updatedAt = rule.UpdatedAt.UnixMilli()
	}
	return &ratelimitv1.Rule{
		TenantId:        rule.TenantID,
		Resource:        rule.Resource,
		Algorithm:       rule.Algorithm,
		Limit:           rule.Limit,
		WindowMs:        rule.Window.Milliseconds(),
		BurstSize:       rule.BurstSize,
		Version:         rule.Version,
		UpdatedAtUnixMs: updatedAt,
	}
}

func grpcErrorCode(code ErrorCode) string {
	if code != "" {
		return string(code)
	}
	return "LIMITER_ERROR"
}
