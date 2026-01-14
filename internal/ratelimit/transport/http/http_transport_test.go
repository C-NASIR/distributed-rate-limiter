package httptransport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/app"
	"ratelimit/internal/ratelimit/config"
	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/observability"
	"ratelimit/internal/ratelimit/store/inmemory"
	httptransport "ratelimit/internal/ratelimit/transport/http"
)

func TestHTTP_Check_ReturnsRuleNotFoundErrorCode(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	server := newHTTPTestServer(t, app)
	defer server.Close()

	payload, err := json.Marshal(httptransport.HTTPCheckRequest{
		TenantID: "tenant",
		UserID:   "user",
		Resource: "resource",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/ratelimit/check", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("failed to post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 got %d", resp.StatusCode)
	}

	var body httptransport.HTTPCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.ErrorCode != "RULE_NOT_FOUND" {
		t.Fatalf("expected RULE_NOT_FOUND got %q", body.ErrorCode)
	}
}

func TestHTTP_Admin_Create_Then_Get(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	server := newHTTPTestServer(t, app)
	defer server.Close()

	create := httptransport.HTTPCreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "create-1",
	}
	createBody, err := json.Marshal(create)
	if err != nil {
		t.Fatalf("failed to marshal create: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/admin/rules", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("failed to post create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201 got %d", resp.StatusCode)
	}

	getResp, err := http.Get(server.URL + "/v1/admin/rules?tenantID=tenant&resource=resource")
	if err != nil {
		t.Fatalf("failed to get rule: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 got %d", getResp.StatusCode)
	}
	var rule httptransport.HTTPRuleResponse
	if err := json.NewDecoder(getResp.Body).Decode(&rule); err != nil {
		t.Fatalf("failed to decode rule: %v", err)
	}
	if rule.TenantID != "tenant" || rule.Resource != "resource" || rule.Limit != 10 {
		t.Fatalf("unexpected rule: %#v", rule)
	}
}

func TestHTTP_Admin_Update_Conflict(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	server := newHTTPTestServer(t, app)
	defer server.Close()

	create := httptransport.HTTPCreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "create-2",
	}
	createBody, err := json.Marshal(create)
	if err != nil {
		t.Fatalf("failed to marshal create: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/admin/rules", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("failed to post create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201 got %d", resp.StatusCode)
	}

	update := httptransport.HTTPUpdateRuleRequest{
		TenantID:        "tenant",
		Resource:        "resource",
		Algorithm:       "token_bucket",
		Limit:           20,
		Window:          time.Second,
		BurstSize:       0,
		ExpectedVersion: 99,
	}
	updateBody, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("failed to marshal update: %v", err)
	}
	request, err := http.NewRequest(http.MethodPut, server.URL+"/v1/admin/rules", bytes.NewReader(updateBody))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	updateResp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("failed to update: %v", err)
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected status 409 got %d", updateResp.StatusCode)
	}
}

func TestHTTP_CheckBatch_PreservesOrder(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	server := newHTTPTestServer(t, app)
	defer server.Close()

	_, err := app.AdminHandler.CreateRule(testContext(), &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource-a",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "batch-1",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}
	_, err = app.AdminHandler.CreateRule(testContext(), &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource-b",
		Algorithm:      "token_bucket",
		Limit:          5,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "batch-2",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}

	batch := []httptransport.HTTPCheckRequest{
		{TenantID: "tenant", UserID: "user", Resource: "resource-b", Cost: 1},
		{TenantID: "tenant", UserID: "user", Resource: "resource-a", Cost: 1},
		{TenantID: "tenant", UserID: "user", Resource: "missing", Cost: 1},
	}
	batchBody, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("failed to marshal batch: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/ratelimit/checkBatch", "application/json", bytes.NewReader(batchBody))
	if err != nil {
		t.Fatalf("failed to post batch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 got %d", resp.StatusCode)
	}
	var responses []httptransport.HTTPCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&responses); err != nil {
		t.Fatalf("failed to decode batch response: %v", err)
	}
	if len(responses) != len(batch) {
		t.Fatalf("expected %d responses got %d", len(batch), len(responses))
	}
	if responses[0].ErrorCode != "" || responses[1].ErrorCode != "" {
		t.Fatalf("expected allowed responses: %#v", responses)
	}
	if responses[2].ErrorCode != "RULE_NOT_FOUND" {
		t.Fatalf("expected rule not found got %q", responses[2].ErrorCode)
	}
}

func newHTTPTestApplication(t *testing.T) *app.Application {
	t.Helper()
	cfg := &config.Config{
		Region: "test",
		RuleDB: inmemory.NewInMemoryRuleDB(nil),
		Outbox: inmemory.NewInMemoryOutbox(),
		PubSub: inmemory.NewInMemoryPubSub(),
	}
	app, err := app.NewApplication(cfg)
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}
	return app
}

func newHTTPTestServer(t *testing.T, appInstance *app.Application) *httptest.Server {
	t.Helper()
	transport := httptransport.NewHTTPTransport(":0", func() bool { return true })
	if err := transport.ServeRateLimit(appInstance.RateLimitHandler); err != nil {
		t.Fatalf("failed to register rate service: %v", err)
	}
	if err := transport.ServeAdmin(appInstance.AdminHandler); err != nil {
		t.Fatalf("failed to register admin service: %v", err)
	}
	transport.Configure(httptransport.HTTPTransportConfig{
		Metrics: observability.NewInMemoryMetrics(),
		Mode:    appInstance.Mode,
		Region:  appInstance.Config.Region,
	})
	handler, err := transport.Handler()
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}
	return httptest.NewServer(handler)
}

func testContext() context.Context {
	return context.Background()
}
