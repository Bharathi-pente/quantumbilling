// Package handler implements the HTTP handlers for the ingest API.
package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/pente/quantumbilling/engine/internal/auth"
	"github.com/pente/quantumbilling/engine/internal/kafka"
	"github.com/pente/quantumbilling/engine/internal/models"
	"github.com/pente/quantumbilling/engine/internal/postgres"
	"github.com/redis/go-redis/v9"
)

// IngestHandler holds dependencies for the ingest endpoints.
type IngestHandler struct {
	Redis    *redis.Client
	PG       *sql.DB
	Log      *slog.Logger
	Publish  kafka.PublishFunc
	PubBatch kafka.BatchPublishFunc
}

// NewIngestHandler creates a handler with the given dependencies.
func NewIngestHandler(rdb *redis.Client, pg *sql.DB, log *slog.Logger, pub kafka.PublishFunc, pubBatch kafka.BatchPublishFunc) *IngestHandler {
	return &IngestHandler{Redis: rdb, PG: pg, Log: log, Publish: pub, PubBatch: pubBatch}
}

// POST /v1/events — single event ingest (story_4)
func (h *IngestHandler) HandleSingleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "only POST is allowed")
		return
	}

	// Content-Type check
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		return
	}

	// Body size limit: 1 MB
	r.Body = http.MaxBytesReader(w, r.Body, models.DefaultMaxBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 1MB limit")
		return
	}

	// Parse event
	var event models.UsageEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse JSON body")
		return
	}

	// Get KeyContext from middleware
	kc, ok := auth.GetKeyContext(r.Context())
	if !ok || kc == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}

	// Enrich from key context (anti-spoofing)
	event.EnrichFromKeyContext(kc)

	// Validate
	if err := event.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	// Idempotency: SETNX idem:{org_id}:{event_id} (24h TTL) — story_4 AC 11-14
	idemKey := fmt.Sprintf("idem:%s:%s", event.OrgID, event.EventID)
	ttl := getEnvDuration("IDEMPOTENCY_TTL", models.DefaultIdempotencyTTL)
	ok, err = h.Redis.SetNX(r.Context(), idemKey, "1", ttl).Result()
	if err != nil {
		h.Log.Error("idempotency check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "idempotency service unavailable")
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "DUPLICATE_EVENT",
			fmt.Sprintf("event %s already processed", event.EventID))
		return
	}

	// Org existence check — Redis cache → Postgres fallback (story_4 AC 15-19)
	if err := h.verifyOrg(r.Context(), event.OrgID); err != nil {
		// Rollback idempotency on validation failure
		h.Redis.Del(r.Context(), idemKey)
		writeError(w, http.StatusForbidden, "UNKNOWN_ORG",
			fmt.Sprintf("organization %s not found", event.OrgID))
		return
	}

	// End-user-in-org check (story_4 AC 20-21)
	if event.EndUserID != "" {
		if err := h.verifyEndUser(r.Context(), event.OrgID, event.EndUserID); err != nil {
			h.Redis.Del(r.Context(), idemKey)
			writeError(w, http.StatusUnprocessableEntity, "END_USER_NOT_IN_ORG",
				fmt.Sprintf("end_user %s does not belong to org %s", event.EndUserID, event.OrgID))
			return
		}
	}

	// Serialize and publish to Kafka (async — 202 accepted)
	msgBytes, err := json.Marshal(event)
	if err != nil {
		h.Redis.Del(r.Context(), idemKey)
		h.Log.Error("marshal failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to serialize event")
		return
	}

	// Publish to Kafka (async — 202 accepted)
	if h.Publish != nil {
		if err := h.Publish(r.Context(), msgBytes, event.OrgID); err != nil {
			h.Log.Error("kafka publish failed", "error", err, "event_id", event.EventID)
			// Event still accepted — Kafka producer handles retries internally.
			// If the producer is permanently down, the health endpoint will reflect it.
		}
	} else {
		h.Log.Warn("kafka producer not configured — event logged but not published",
			"event_id", event.EventID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"accepted": true,
		"event_id": event.EventID,
		"message":  "event accepted for processing",
	})
}

// verifyOrg checks org existence: Redis cache → Postgres fallback → backfill.
func (h *IngestHandler) verifyOrg(ctx context.Context, orgID string) error {
	// Redis existence cache
	exists, err := h.Redis.Exists(ctx, "org:"+orgID).Result()
	if err == nil && exists > 0 {
		return nil
	}

	// Postgres fallback
	if h.PG != nil {
		ok, pgErr := postgres.OrgExists(ctx, h.PG, orgID)
		if pgErr != nil {
			h.Log.Warn("org postgres fallback failed", "org_id", orgID, "error", pgErr)
			return pgErr
		}
		if ok {
			// Backfill Redis cache (1h TTL)
			h.Redis.Set(ctx, "org:"+orgID, "1", 1*time.Hour)
			return nil
		}
	}

	return fmt.Errorf("org not found: %s", orgID)
}

// verifyEndUser checks end-user-in-org: Redis → Postgres fallback.
func (h *IngestHandler) verifyEndUser(ctx context.Context, orgID, endUserID string) error {
	euKey := fmt.Sprintf("org:%s:enduser:%s", orgID, endUserID)
	exists, err := h.Redis.Exists(ctx, euKey).Result()
	if err == nil && exists > 0 {
		return nil
	}

	if h.PG != nil {
		ok, pgErr := postgres.EndUserInOrg(ctx, h.PG, orgID, endUserID)
		if pgErr != nil {
			h.Log.Warn("end-user postgres fallback failed", "org_id", orgID, "end_user_id", endUserID, "error", pgErr)
			return pgErr
		}
		if ok {
			h.Redis.Set(ctx, euKey, "1", 1*time.Hour)
			return nil
		}
	}

	return fmt.Errorf("end_user not in org: %s / %s", endUserID, orgID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if s := os.Getenv(key); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		if secs, err := strconv.Atoi(s); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultVal
}
