// Analytics API: Phase 4 read-path service — 18 endpoints over ClickHouse dedup view.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/pente/quantumbilling/engine/internal/analytics"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := analytics.NewService(logger)

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ready"}`)
	})

	// Org endpoints (story_15)
	mux.HandleFunc("/v1/orgs/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case match(path, "/v1/orgs/*/summary"):
			svc.OrgSummary(w, r)
		case match(path, "/v1/orgs/*/customers/usage"):
			svc.OrgCustomersUsage(w, r)
		case match(path, "/v1/orgs/*/models/usage"):
			svc.OrgModelsUsage(w, r)
		case match(path, "/v1/orgs/*/services/usage"):
			svc.OrgServicesUsage(w, r)
		case match(path, "/v1/orgs/*/cost"):
			svc.OrgCost(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// Customer endpoints (story_15 + story_19)
	mux.HandleFunc("/v1/customers/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case match(path, "/v1/customers/*/summary"):
			svc.CustomerSummary(w, r)
		case match(path, "/v1/customers/*/end-users/usage"):
			svc.CustomerEndUsersUsage(w, r)
		case match(path, "/v1/customers/*/models/usage"):
			svc.CustomerModelsUsage(w, r)
		case match(path, "/v1/customers/*/cost"):
			svc.CustomerCost(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// End-user endpoints (story_16)
	mux.HandleFunc("/v1/end-users/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case match(path, "/v1/end-users/*/summary"):
			svc.EndUserSummary(w, r)
		case match(path, "/v1/end-users/*/daily"):
			svc.EndUserDaily(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// Trend endpoints (story_17) — hourly, daily, weekly, monthly
	mux.HandleFunc("/v1/analytics/hourly", svc.TrendHandler("hourly"))
	mux.HandleFunc("/v1/analytics/daily", svc.TrendHandler("daily"))
	mux.HandleFunc("/v1/analytics/weekly", svc.TrendHandler("weekly"))
	mux.HandleFunc("/v1/analytics/monthly", svc.TrendHandler("monthly"))

	// Model & Service endpoints (story_18)
	mux.HandleFunc("/v1/analytics/models/usage", svc.ModelsUsage)
	mux.HandleFunc("/v1/analytics/services/usage", svc.ServicesUsage)

	// Cost endpoint (story_19)
	mux.HandleFunc("/v1/analytics/cost", svc.CostReport)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8014"
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down analytics-api...")
		os.Exit(0)
	}()

	logger.Info("analytics-api listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.Error("server error", "error", err)
	}
}

func match(path, pattern string) bool {
	// Simple wildcard matching for /v1/orgs/{id}/summary etc.
	parts := splitPath(path)
	pParts := splitPath(pattern)
	if len(parts) != len(pParts) {
		return false
	}
	for i, p := range pParts {
		if p == "*" {
			continue // wildcard matches anything
		}
		if p != parts[i] {
			return false
		}
	}
	return true
}

func splitPath(path string) []string {
	s := path
	if len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	if len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return nil
	}
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
