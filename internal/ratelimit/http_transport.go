// Package ratelimit provides an HTTP transport.
package ratelimit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

// HTTPTransport serves the RateLimit and Admin APIs over HTTP.
type HTTPTransport struct {
	addr     string
	srv      *http.Server
	rate     RateLimitService
	admin    AdminService
	appReady func() bool
	metrics  *InMemoryMetrics
	mode     func() OperatingMode
	mux      http.Handler
	mu       sync.Mutex
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
func (t *HTTPTransport) ServeRateLimit(service RateLimitService) error {
	if service == nil {
		return errors.New("rate limit service is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate = service
	return nil
}

// ServeAdmin registers the admin service.
func (t *HTTPTransport) ServeAdmin(service AdminService) error {
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
		t.srv = &http.Server{Addr: t.addr, Handler: handler}
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
