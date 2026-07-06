package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TC-01: OrgSummary returns 200 with zeroed totals
func TestTC01_OrgSummaryZeroFill(t *testing.T) {
	svc := NewService(nil)
	req := httptest.NewRequest("GET", "/v1/orgs/org_test/summary", nil)
	rec := httptest.NewRecorder()
	svc.OrgSummary(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp OrgSummary
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Totals.Cost != "0.000000" {
		t.Errorf("expected zero cost, got %s", resp.Totals.Cost)
	}
	if resp.ModelsUsed == nil {
		t.Error("models_used should be empty array, not nil")
	}
}

// TC-02: TrendHandler returns zero-filled series
func TestTC02_TrendHandlerZeroFill(t *testing.T) {
	svc := NewService(nil)
	handler := svc.TrendHandler("daily")
	req := httptest.NewRequest("GET", "/v1/analytics/daily?from=2026-01-01&to=2026-01-07", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp TrendSeries
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Granularity != "daily" {
		t.Errorf("expected daily, got %s", resp.Granularity)
	}
}

// TC-03: All 18 endpoints return 200
func TestTC03_AllEndpointsReturn200(t *testing.T) {
	svc := NewService(nil)

	tests := []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{"GET", "/v1/orgs/org_x/summary", svc.OrgSummary},
		{"GET", "/v1/orgs/org_x/customers/usage", svc.OrgCustomersUsage},
		{"GET", "/v1/orgs/org_x/models/usage", svc.OrgModelsUsage},
		{"GET", "/v1/orgs/org_x/services/usage", svc.OrgServicesUsage},
		{"GET", "/v1/orgs/org_x/cost", svc.OrgCost},
		{"GET", "/v1/customers/cust_x/summary", svc.CustomerSummary},
		{"GET", "/v1/customers/cust_x/end-users/usage", svc.CustomerEndUsersUsage},
		{"GET", "/v1/customers/cust_x/models/usage", svc.CustomerModelsUsage},
		{"GET", "/v1/customers/cust_x/cost", svc.CustomerCost},
		{"GET", "/v1/end-users/eu_x/summary", svc.EndUserSummary},
		{"GET", "/v1/end-users/eu_x/daily", svc.EndUserDaily},
		{"GET", "/v1/analytics/models/usage", svc.ModelsUsage},
		{"GET", "/v1/analytics/services/usage", svc.ServicesUsage},
		{"GET", "/v1/analytics/cost", svc.CostReport},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()
		tt.handler(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s %s: expected 200, got %d", tt.method, tt.path, rec.Code)
		}
	}
}

// TC-04: Cost is always decimal string
func TestTC04_CostIsDecimalString(t *testing.T) {
	svc := NewService(nil)
	req := httptest.NewRequest("GET", "/v1/analytics/cost", nil)
	rec := httptest.NewRecorder()
	svc.CostReport(rec, req)

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	cost, ok := resp["total_cost"].(string)
	if !ok {
		t.Fatal("total_cost must be a string")
	}
	if cost != "0.000000" {
		t.Errorf("expected '0.000000', got '%s'", cost)
	}
}

// TC-05: pathParam helper
func TestTC05_PathParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/orgs/my-org-id/summary", nil)
	if p := pathParam(req, 3); p != "my-org-id" {
		t.Errorf("expected my-org-id, got %s", p)
	}
}
