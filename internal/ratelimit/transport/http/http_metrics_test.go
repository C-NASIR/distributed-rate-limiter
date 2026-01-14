package httptransport_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"ratelimit/internal/ratelimit/core"
	httptransport "ratelimit/internal/ratelimit/transport/http"
)

func TestHTTP_MetricsEndpoint(t *testing.T) {
	t.Parallel()

	app := newHTTPTestApplication(t)
	server := newHTTPTestServer(t, app)
	defer server.Close()

	_, err := app.AdminHandler.CreateRule(testContext(), &core.CreateRuleRequest{
		TenantID:       "tenant",
		Resource:       "resource",
		Algorithm:      "token_bucket",
		Limit:          10,
		Window:         time.Second,
		BurstSize:      0,
		IdempotencyKey: "metrics-1",
	})
	if err != nil {
		t.Fatalf("failed to create rule: %v", err)
	}

	checkBody, err := json.Marshal(httptransport.HTTPCheckRequest{
		TenantID: "tenant",
		UserID:   "user",
		Resource: "resource",
		Cost:     1,
	})
	if err != nil {
		t.Fatalf("failed to marshal check: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/ratelimit/check", "application/json", bytes.NewReader(checkBody))
	if err != nil {
		t.Fatalf("failed to post check: %v", err)
	}
	resp.Body.Close()

	metricsResp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to get metrics: %v", err)
	}
	defer metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 got %d", metricsResp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(metricsResp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode metrics: %v", err)
	}
	counters, ok := payload["counters"].(map[string]any)
	if !ok || len(counters) == 0 {
		t.Fatalf("expected counters in metrics: %#v", payload)
	}
}
