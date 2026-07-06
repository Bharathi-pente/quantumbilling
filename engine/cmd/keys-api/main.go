// Keys API: key generation, revocation, listing, BYOK registration (Phase 3 / D-05).
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pente/quantumbilling/engine/internal/byok"
	"github.com/pente/quantumbilling/engine/internal/keys"
	"github.com/pente/quantumbilling/engine/internal/security"
	"github.com/redis/go-redis/v9"

	_ "github.com/lib/pq"
)

var (
	logger      *slog.Logger
	keySvc      *keys.Service
	byokSvc     *byok.Service
	auditLogger *security.AuditLogger
)

func main() {
	logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pgDSN := os.Getenv("DATABASE_URL")
	if pgDSN == "" {
		pgDSN = "postgresql://quantum:quantum-dev-password@localhost:5432/quantumbilling?sslmode=disable"
	}
	pg, err := sql.Open("postgres", pgDSN)
	if err != nil {
		logger.Error("postgres connect failed", "error", err)
		os.Exit(1)
	}
	pg.SetMaxOpenConns(10)

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: 2 * time.Second})

	keySvc = keys.NewService(pg, rdb, logger)
	byokSvc = byok.NewService(pg, logger)
	auditLogger = security.NewAuditLogger(pg, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)
	mux.HandleFunc("/v1/keys", handleKeys)
	mux.HandleFunc("/v1/keys/", handleKeyByID)
	mux.HandleFunc("/v1/byok/config", handleBYOK)
	mux.HandleFunc("/v1/security-audit-logs", handleSecurityAuditLogs)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8013"
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down keys-api...")
		os.Exit(0)
	}()

	logger.Info("keys-api listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.Error("server error", "error", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ready"}`)
}

// POST /v1/keys — create key (story_11)
// GET /v1/keys — list keys (story_12)
func handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req keys.KeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": "INVALID_JSON", "message": "failed to parse body"}})
			return
		}
		resp, err := keySvc.CreateKey(r.Context(), req)
		if err != nil {
			code := "BAD_REQUEST"
			if contains(err.Error(), "INVALID_") {
				code = extractCode(err.Error())
			}
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": code, "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusCreated, resp)

	case http.MethodGet:
		orgID := r.URL.Query().Get("org_id")
		if orgID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": "MISSING_ORG_ID", "message": "org_id query param required"}})
			return
		}
		items, err := keySvc.ListKeys(r.Context(), orgID, 100, 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": map[string]string{"code": "INTERNAL_ERROR", "message": err.Error()}})
			return
		}
		writeJSON(w, http.StatusOK, items)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": map[string]string{"code": "METHOD_NOT_ALLOWED", "message": "only GET/POST"}})
	}
}

// DELETE /v1/keys/{id} — revoke key (story_12)
func handleKeyByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": map[string]string{"code": "METHOD_NOT_ALLOWED", "message": "only DELETE"}})
		return
	}
	keyID := r.URL.Path[len("/v1/keys/"):]
	if keyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": "MISSING_KEY_ID", "message": "key ID required"}})
		return
	}
	if err := keySvc.RevokeKey(r.Context(), keyID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": map[string]string{"code": "NOT_FOUND", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// POST /v1/byok/config — register BYOK provider key (story_13)
func handleBYOK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": map[string]string{"code": "METHOD_NOT_ALLOWED", "message": "only POST"}})
		return
	}
	var req struct {
		OrgID    string `json:"org_id"`
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": "INVALID_JSON", "message": "failed to parse body"}})
		return
	}
	if err := byokSvc.RegisterProviderKey(r.Context(), req.OrgID, req.Provider, req.APIKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{"code": extractCode(err.Error()), "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

// GET /v1/security-audit-logs (story_14)
func handleSecurityAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": map[string]string{"code": "METHOD_NOT_ALLOWED", "message": "only GET"}})
		return
	}
	// Placeholder: query audit.security_audit_logs with filters
	writeJSON(w, http.StatusOK, []map[string]string{})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSub(s, substr)))
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func extractCode(msg string) string {
	if idx := findIndex(msg, ":"); idx > 0 {
		return msg[:idx]
	}
	return "BAD_REQUEST"
}

func findIndex(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
