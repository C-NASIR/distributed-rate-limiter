// Package httptransport provides an HTTP transport.
package httptransport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/observability"
)

// HTTPTransport serves the RateLimit and Admin APIs over HTTP.
type HTTPTransport struct {
	addr         string
	srv          *http.Server
	rate         core.RateLimitService
	admin        core.AdminService
	appReady     func() bool
	metrics      *observability.InMemoryMetrics
	region       string
	mode         func() core.OperatingMode
	mux          http.Handler
	mu           sync.Mutex
	readTimeout  time.Duration
	writeTimeout time.Duration
	idleTimeout  time.Duration
	maxBodyBytes int64
	enableAuth   bool
	adminToken   string
	logger       observability.Logger
}

// HTTPTransportConfig configures the HTTP transport.
type HTTPTransportConfig struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	MaxBodyBytes int64
	EnableAuth   bool
	AdminToken   string
	Logger       observability.Logger
	Metrics      *observability.InMemoryMetrics
	Region       string
	Mode         func() core.OperatingMode
}

// NewHTTPTransport constructs a transport bound to an address.
func NewHTTPTransport(addr string, ready func() bool) *HTTPTransport {
	if addr == "" {
		addr = ":8080"
	}
	if ready == nil {
		ready = func() bool { return false }
	}
	return &HTTPTransport{addr: addr, appReady: ready}
}

// ServeRateLimit registers the rate limit service.
func (t *HTTPTransport) ServeRateLimit(service core.RateLimitService) error {
	if service == nil {
		return errors.New("rate limit service is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate = service
	return nil
}

// ServeAdmin registers the admin service.
func (t *HTTPTransport) ServeAdmin(service core.AdminService) error {
	if service == nil {
		return errors.New("admin service is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.admin = service
	return nil
}

// Start begins serving HTTP requests.
func (t *HTTPTransport) Start() error {
	if t == nil {
		return errors.New("http transport is nil")
	}
	handler, err := t.handler()
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.srv == nil {
		t.srv = &http.Server{
			Addr:         t.addr,
			Handler:      handler,
			ReadTimeout:  t.readTimeout,
			WriteTimeout: t.writeTimeout,
			IdleTimeout:  t.idleTimeout,
		}
	}
	srv := t.srv
	t.mu.Unlock()

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Configure applies transport configuration values.
func (t *HTTPTransport) Configure(cfg HTTPTransportConfig) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.readTimeout = cfg.ReadTimeout
	t.writeTimeout = cfg.WriteTimeout
	t.idleTimeout = cfg.IdleTimeout
	if cfg.MaxBodyBytes > 0 {
		t.maxBodyBytes = cfg.MaxBodyBytes
	}
	t.enableAuth = cfg.EnableAuth
	t.adminToken = cfg.AdminToken
	t.logger = cfg.Logger
	t.metrics = cfg.Metrics
	t.region = cfg.Region
	t.mode = cfg.Mode
}

// Shutdown stops the HTTP server.
func (t *HTTPTransport) Shutdown(ctx context.Context) error {
	if t == nil {
		return errors.New("http transport is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	srv := t.srv
	t.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Handler returns the HTTP handler for testing.
func (t *HTTPTransport) Handler() (http.Handler, error) {
	return t.handler()
}

func (t *HTTPTransport) handler() (http.Handler, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mux != nil {
		return t.mux, nil
	}
	if t.rate == nil || t.admin == nil {
		return nil, errors.New("services must be registered before starting")
	}
	mux := http.NewServeMux()
	t.registerRoutes(mux)
	t.mux = mux
	return mux, nil
}
