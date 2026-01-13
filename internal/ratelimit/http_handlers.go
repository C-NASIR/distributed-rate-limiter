// Package ratelimit provides HTTP handlers.
package ratelimit

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

const defaultMaxBodyBytes = 1 << 20

type httpErrorResponse struct {
	Error string `json:"error"`
}

func (t *HTTPTransport) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/ratelimit/check", t.handleCheck)
	mux.HandleFunc("/v1/ratelimit/checkBatch", t.handleCheckBatch)
	mux.HandleFunc("/v1/admin/rules", t.handleRules)
	mux.HandleFunc("/v1/admin/rules/list", t.handleRulesList)
	mux.HandleFunc("/healthz", t.handleHealth)
	mux.HandleFunc("/readyz", t.handleReady)
	mux.HandleFunc("/metrics", t.handleMetrics)
	mux.HandleFunc("/mode", t.handleMode)
}

func (t *HTTPTransport) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	defer func() {
		if t.metrics != nil {
			t.metrics.ObserveLatency("httpCheck", time.Since(start), regionLabel(t.region))
		}
	}()
	var httpReq httpCheckRequest
	if err := t.decodeJSON(w, r, &httpReq); err != nil {
		t.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if httpReq.TenantID == "" || httpReq.UserID == "" || httpReq.Resource == "" || httpReq.Cost <= 0 {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	resp, err := t.rate.CheckLimit(r.Context(), toCheckLimitRequest(httpReq))
	if err != nil {
		switch CodeOf(err) {
		case CodeInvalidInput, CodeInvalidCost:
			t.writeError(w, r, http.StatusBadRequest, err)
		default:
			t.writeError(w, r, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, fromCheckLimitResponse(resp))
}

func (t *HTTPTransport) handleCheckBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	defer func() {
		if t.metrics != nil {
			t.metrics.ObserveLatency("httpCheckBatch", time.Since(start), regionLabel(t.region))
		}
	}()
	var httpReqs []httpCheckRequest
	if err := t.decodeJSON(w, r, &httpReqs); err != nil {
		t.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	requests := make([]*CheckLimitRequest, len(httpReqs))
	for i, req := range httpReqs {
		requests[i] = toCheckLimitRequest(req)
	}
	responses, err := t.rate.CheckLimitBatch(r.Context(), requests)
	if err != nil {
		switch CodeOf(err) {
		case CodeInvalidInput, CodeInvalidCost:
			t.writeError(w, r, http.StatusBadRequest, err)
		default:
			t.writeError(w, r, http.StatusInternalServerError, err)
		}
		return
	}
	result := make([]httpCheckResponse, len(responses))
	for i, resp := range responses {
		result[i] = fromCheckLimitResponse(resp)
	}
	writeJSON(w, http.StatusOK, result)
}

func (t *HTTPTransport) handleRules(w http.ResponseWriter, r *http.Request) {
	if !t.authorizeAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		t.handleCreateRule(w, r)
	case http.MethodPut:
		t.handleUpdateRule(w, r)
	case http.MethodDelete:
		t.handleDeleteRule(w, r)
	case http.MethodGet:
		t.handleGetRule(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (t *HTTPTransport) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var httpReq httpCreateRuleRequest
	if err := t.decodeJSON(w, r, &httpReq); err != nil {
		t.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if httpReq.TenantID == "" || httpReq.Resource == "" || httpReq.Algorithm == "" || httpReq.Limit <= 0 {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	rule, err := t.admin.CreateRule(r.Context(), toCreateRuleRequest(httpReq))
	if err != nil {
		t.writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, fromRule(rule))
}

func (t *HTTPTransport) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	var httpReq httpUpdateRuleRequest
	if err := t.decodeJSON(w, r, &httpReq); err != nil {
		t.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if httpReq.TenantID == "" || httpReq.Resource == "" || httpReq.Algorithm == "" || httpReq.Limit <= 0 {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	rule, err := t.admin.UpdateRule(r.Context(), toUpdateRuleRequest(httpReq))
	if err != nil {
		t.writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fromRule(rule))
}

func (t *HTTPTransport) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	tenantID := query.Get("tenantID")
	resource := query.Get("resource")
	versionStr := query.Get("expectedVersion")
	if tenantID == "" || resource == "" || versionStr == "" {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	expectedVersion, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	if err := t.admin.DeleteRule(r.Context(), tenantID, resource, expectedVersion); err != nil {
		t.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *HTTPTransport) handleGetRule(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	tenantID := query.Get("tenantID")
	resource := query.Get("resource")
	if tenantID == "" || resource == "" {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	rule, err := t.admin.GetRule(r.Context(), tenantID, resource)
	if err != nil {
		t.writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fromRule(rule))
}

func (t *HTTPTransport) handleRulesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !t.authorizeAdmin(w, r) {
		return
	}
	tenantID := r.URL.Query().Get("tenantID")
	if tenantID == "" {
		t.writeError(w, r, http.StatusBadRequest, ErrInvalidInput)
		return
	}
	rules, err := t.admin.ListRules(r.Context(), tenantID)
	if err != nil {
		t.writeAdminError(w, r, err)
		return
	}
	resp := make([]httpRuleResponse, len(rules))
	for i, rule := range rules {
		resp[i] = fromRule(rule)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (t *HTTPTransport) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (t *HTTPTransport) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if t.appReady != nil && t.appReady() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
}

func (t *HTTPTransport) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if t.metrics == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, t.metrics.Snapshot())
}

func (t *HTTPTransport) handleMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mode := ModeNormal
	if t.mode != nil {
		mode = t.mode()
	}
	label := "normal"
	switch mode {
	case ModeDegraded:
		label = "degraded"
	case ModeEmergency:
		label = "emergency"
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": label})
}

func (t *HTTPTransport) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return ErrInvalidInput
	}
	maxBytes := t.maxBodyBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBodyBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return ErrInvalidInput
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalidInput
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (t *HTTPTransport) writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	if t != nil {
		t.logRequestError(r, status, err)
	}
	writeJSON(w, status, httpErrorResponse{Error: err.Error()})
}

func (t *HTTPTransport) writeAdminError(w http.ResponseWriter, r *http.Request, err error) {
	status := statusForCode(CodeOf(err))
	t.writeError(w, r, status, err)
}

func statusForCode(code ErrorCode) int {
	switch code {
	case CodeInvalidInput, CodeInvalidCost:
		return http.StatusBadRequest
	case CodeConflict:
		return http.StatusConflict
	case CodeNotFound:
		return http.StatusNotFound
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func (t *HTTPTransport) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	if t == nil || !t.enableAuth {
		return true
	}
	expected := "Bearer " + t.adminToken
	if r.Header.Get("Authorization") != expected {
		t.writeError(w, r, http.StatusUnauthorized, Wrap(CodeUnauthorized, "unauthorized", nil))
		return false
	}
	return true
}

func (t *HTTPTransport) logRequestError(r *http.Request, status int, err error) {
	if t == nil || t.logger == nil || r == nil || err == nil {
		return
	}
	fields := map[string]any{
		"method": r.Method,
		"path":   r.URL.Path,
		"status": status,
		"error":  err.Error(),
	}
	if status >= http.StatusInternalServerError {
		t.logger.Error("http request error", fields)
		return
	}
	t.logger.Info("http request error", fields)
}
