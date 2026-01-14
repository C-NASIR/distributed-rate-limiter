package httptransport_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httptransport "ratelimit/internal/ratelimit/transport/http"
)

func TestHTTP_AdminAuth_RejectsWithoutToken(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport := httptransport.NewHTTPTransport(":0", func() bool { return true })
	transport.Configure(httptransport.HTTPTransportConfig{EnableAuth: true, AdminToken: "token"})
	if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
		t.Fatalf("failed to register rate service: %v", err)
	}
	if err := transport.ServeAdmin(app.AdminHandler); err != nil {
		t.Fatalf("failed to register admin service: %v", err)
	}
	handler, err := transport.Handler()
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	create := httptransport.HTTPCreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "auth-1",
	}
	createBody, err := json.Marshal(create)
	if err != nil {
		t.Fatalf("failed to marshal create: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/admin/rules", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("failed to post create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401 got %d", resp.StatusCode)
	}
}

func TestHTTP_AdminAuth_AllowsWithToken(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	transport := httptransport.NewHTTPTransport(":0", func() bool { return true })
	transport.Configure(httptransport.HTTPTransportConfig{EnableAuth: true, AdminToken: "token"})
	if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
		t.Fatalf("failed to register rate service: %v", err)
	}
	if err := transport.ServeAdmin(app.AdminHandler); err != nil {
		t.Fatalf("failed to register admin service: %v", err)
	}
	handler, err := transport.Handler()
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	create := httptransport.HTTPCreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "auth-2",
	}
	createBody, err := json.Marshal(create)
	if err != nil {
		t.Fatalf("failed to marshal create: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/admin/rules", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("failed to post create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201 got %d", resp.StatusCode)
	}
}
