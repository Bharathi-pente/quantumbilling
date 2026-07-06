package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/pente/quantumbilling/engine/internal/auth"
	"github.com/pente/quantumbilling/engine/internal/models"
	"github.com/redis/go-redis/v9"
)

// BatchResult holds per-event batch processing results.
type BatchResult struct {
	Accepted       []models.UsageEvent
	FailedCount    int
	DuplicateCount int
	UnknownOrgCount int
	UserNotInOrgCount int
	Errors         []BatchError
}

// BatchError represents a per-index error in the batch.
type BatchError struct {
	Index int    `json:"index"`
	Code  string `json:"code"`
	EventID string `json:"event_id,omitempty"`
}

// HandleBatchEvent handles POST /v1/events/batch (story_5).
func (h *IngestHandler) HandleBatchEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "only POST is allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, models.DefaultMaxBatchBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 500MB limit")
		return
	}

	// Parse batch (wrapped or bare)
	events, err := models.ParseIngestBatch(body)
	if err != nil || len(events) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse batch body")
		return
	}

	// Size check
	maxBatch := getEnvInt("MAX_BATCH_SIZE", models.DefaultMaxBatchSize)
	if len(events) > maxBatch {
		writeError(w, http.StatusRequestEntityTooLarge, "BATCH_TOO_LARGE",
			fmt.Sprintf("batch size %d exceeds max %d", len(events), maxBatch))
		return
	}

	// Auth context
	kc, ok := auth.GetKeyContext(r.Context())
	if !ok || kc == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}

	start := time.Now()

	// Step 1: Enrich all events from key context
	for i := range events {
		events[i].EnrichFromKeyContext(kc)
	}

	// Step 2: Collect event IDs for Bloom pre-filter
	eventIDs := make([]string, len(events))
	for i, e := range events {
		eventIDs[i] = e.EventID
	}

	// Step 3: Batch org lookup
	orgIDs := collectUniqueOrgIDs(events)
	validOrgs := h.batchOrgCheck(r.Context(), orgIDs)

	// Step 4: Batch end-user lookup
	euPairs := collectUniqueEUPairs(events)
	validEUs := h.batchEUCheck(r.Context(), euPairs)

	// Step 5: Bloom + SETNX dedup, filter events
	result := h.processBatchEvents(r.Context(), events, eventIDs, validOrgs, validEUs)

	// Step 6: Publish valid events
	if len(result.Accepted) == 0 {
		writeError(w, http.StatusBadRequest, "NO_VALID_EVENTS", "no valid events in batch")
		return
	}

	// Serialize and publish (Kafka placeholder — async batch produce)
	// TODO: real Kafka batch publish
	_ = result.Accepted

	h.Log.Info("batch processed",
		"batch_size", len(events),
		"accepted_count", len(result.Accepted),
		"failed_count", result.FailedCount,
		"duplicate_count", result.DuplicateCount,
		"unknown_org_count", result.UnknownOrgCount,
		"user_not_in_org_count", result.UserNotInOrgCount,
		"org_id", kc.OrgID,
		"source_mode", kc.SourceMode,
		"latency_ms", time.Since(start).Milliseconds(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"accepted":       true,
		"accepted_count": len(result.Accepted),
		"failed_count":   result.FailedCount,
		"message":        "batch processed",
	})
}

// processBatchEvents runs Bloom pre-filter → SETNX dedup, then filters by org/end-user validity.
func (h *IngestHandler) processBatchEvents(ctx context.Context, events []models.UsageEvent, eventIDs []string, validOrgs map[string]bool, validEUs map[string]bool) BatchResult {
	var result BatchResult
	bloomShards := getEnvInt("BLOOM_NUM_SHARDS", 16)

	for i, event := range events {
		// Org check
		if !validOrgs[event.OrgID] {
			result.FailedCount++
			result.UnknownOrgCount++
			continue
		}
		// End-user check
		if event.EndUserID != "" && !validEUs[event.OrgID+":"+event.EndUserID] {
			result.FailedCount++
			result.UserNotInOrgCount++
			continue
		}

		// Bloom pre-filter
		shard := hashEventID(event.EventID) % bloomShards
		bfKey := fmt.Sprintf("bf:%s:%d", event.OrgID, shard)

		// Check Bloom
		exists, err := h.Redis.Do(ctx, "BF.EXISTS", bfKey, eventIDs[i]).Int()
		if err == nil && exists == 1 {
			// Might be duplicate — full SETNX check
			idemKey := fmt.Sprintf("idem:%s:%s", event.OrgID, event.EventID)
			set, setErr := h.Redis.SetNX(ctx, idemKey, "1", models.DefaultIdempotencyTTL).Result()
			if setErr != nil || !set {
				result.FailedCount++
				result.DuplicateCount++
				continue
			}
		}

		// Add to Bloom
		h.Redis.Do(ctx, "BF.ADD", bfKey, eventIDs[i])

		// SETNX if Bloom said "definitely not seen" (new event)
		if exists == 0 {
			idemKey := fmt.Sprintf("idem:%s:%s", event.OrgID, event.EventID)
			h.Redis.SetNX(ctx, idemKey, "1", models.DefaultIdempotencyTTL)
		}

		result.Accepted = append(result.Accepted, event)
	}

	return result
}

// batchOrgCheck checks multiple org IDs in Redis, falling back to Postgres in bulk.
func (h *IngestHandler) batchOrgCheck(ctx context.Context, orgIDs []string) map[string]bool {
	result := make(map[string]bool, len(orgIDs))
	if len(orgIDs) == 0 {
		return result
	}

	// Pipeline Redis check
	pipe := h.Redis.Pipeline()
	cmds := make([]*redis.IntCmd, len(orgIDs))
	for i, orgID := range orgIDs {
		cmds[i] = pipe.Exists(ctx, "org:"+orgID)
	}
	pipe.Exec(ctx)

	var missing []string
	for i, cmd := range cmds {
		val, _ := cmd.Result()
		if val > 0 {
			result[orgIDs[i]] = true
		} else {
			missing = append(missing, orgIDs[i])
		}
	}

	// Postgres fallback for missing in batch
	if len(missing) > 0 && h.PG != nil {
		pgOrgs := batchOrgPostgres(ctx, h.PG, missing)
		for _, orgID := range pgOrgs {
			result[orgID] = true
			h.Redis.Set(ctx, "org:"+orgID, "1", 1*time.Hour)
		}
	}

	return result
}

// batchEUCheck checks end-user pairs in Redis pipeline, Postgres fallback.
func (h *IngestHandler) batchEUCheck(ctx context.Context, pairs []string) map[string]bool {
	result := make(map[string]bool, len(pairs))
	if len(pairs) == 0 {
		return result
	}

	orgIDs := make([]string, len(pairs))
	euIDs := make([]string, len(pairs))
	for i, pair := range pairs {
		// pair format: "org_id:end_user_id"
		parts := splitPair(pair)
		orgIDs[i] = parts[0]
		euIDs[i] = parts[1]
	}

	pipe := h.Redis.Pipeline()
	cmds := make([]*redis.IntCmd, len(pairs))
	for i := range pairs {
		cmds[i] = pipe.Exists(ctx, fmt.Sprintf("org:%s:enduser:%s", orgIDs[i], euIDs[i]))
	}
	pipe.Exec(ctx)

	var missingOrgs, missingEUs []string
	for i, cmd := range cmds {
		val, _ := cmd.Result()
		if val > 0 {
			result[pairs[i]] = true
		} else {
			missingOrgs = append(missingOrgs, orgIDs[i])
			missingEUs = append(missingEUs, euIDs[i])
		}
	}

	if len(missingOrgs) > 0 && h.PG != nil {
		pgEUs := batchEUPG(ctx, h.PG, missingOrgs, missingEUs)
		for _, eu := range pgEUs {
			key := eu.OrgID + ":" + eu.EndUserID
			result[key] = true
			h.Redis.Set(ctx, "org:"+eu.OrgID+":enduser:"+eu.EndUserID, "1", 1*time.Hour)
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func collectUniqueOrgIDs(events []models.UsageEvent) []string {
	seen := make(map[string]bool)
	var result []string
	for _, e := range events {
		if !seen[e.OrgID] {
			seen[e.OrgID] = true
			result = append(result, e.OrgID)
		}
	}
	return result
}

func collectUniqueEUPairs(events []models.UsageEvent) []string {
	seen := make(map[string]bool)
	var result []string
	for _, e := range events {
		if e.EndUserID == "" {
			continue
		}
		key := e.OrgID + ":" + e.EndUserID
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

func splitPair(pair string) [2]string {
	for i := len(pair) - 1; i >= 0; i-- {
		if pair[i] == ':' {
			return [2]string{pair[:i], pair[i+1:]}
		}
	}
	return [2]string{pair, ""}
}

func hashEventID(eventID string) int {
	h := fnv.New32a()
	h.Write([]byte(eventID))
	return int(h.Sum32())
}

func getEnvInt(key string, defaultVal int) int {
	if s := os.Getenv(key); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return defaultVal
}

// batchOrgPostgres queries multiple org IDs in one query.
func batchOrgPostgres(ctx context.Context, db interface{ QueryRowContext(...interface{}) }, orgIDs []string) []string {
	// Placeholder — real impl uses db.QueryContext with ANY($1)
	_ = ctx
	_ = db
	_ = orgIDs
	return nil
}

// batchEUPG queries end-user pairs in bulk.
type EURecord struct{ OrgID, EndUserID string }

func batchEUPG(ctx context.Context, db interface{ QueryRowContext(...interface{}) }, orgIDs, euIDs []string) []EURecord {
	_ = ctx
	_ = db
	_ = orgIDs
	_ = euIDs
	return nil
}
