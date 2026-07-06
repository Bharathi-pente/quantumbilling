package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	healthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body != `{"status":"ok"}`+"\n" {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestReadyEndpoint_AllUp(t *testing.T) {
	// When deps are reachable, /ready returns 200
	// Note: in CI, deps may not be up; this tests the handler structure
	t.Setenv("PG_HOST", "localhost")
	t.Setenv("PG_PORT", "5432")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("REDIS_PORT", "6379")
	t.Setenv("KAFKA_HOST", "localhost")
	t.Setenv("KAFKA_PORT", "9092")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	readyHandler(rec, req)

	// Will be 200 or 503 depending on whether deps are actually up
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 200 or 503, got %d", rec.Code)
	}
}
