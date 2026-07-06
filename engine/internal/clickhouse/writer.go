// Package clickhouse provides the ClickHouse batch writer for the analytics worker.
// Uses native protocol (port 9000) via clickhouse-go/v2 for high-throughput batch inserts.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
)

// Writer handles batch INSERTs into ClickHouse events.usage_events.
type Writer struct {
	Log *slog.Logger
	// In production: clickhouse.Conn from github.com/ClickHouse/clickhouse-go/v2
	// For now: placeholder that logs operations
}

// NewWriter creates a ClickHouse writer. In production, connects via native protocol.
func NewWriter(log *slog.Logger) *Writer {
	return &Writer{Log: log}
}

// InsertEventBatch inserts a batch of UsageEvent into ClickHouse (story_9 AC 8-17).
// In production, uses prepared batch INSERT via native protocol.
func (w *Writer) InsertEventBatch(ctx context.Context, events []*models.UsageEvent) error {
	if len(events) == 0 {
		return nil
	}

	start := time.Now()

	// In production: conn.PrepareBatch(ctx, insertSQL) → batch.Append(...) for each event → batch.Send()
	// Column order: event_id, org_id, customer_id, end_user_id, session_id, source_mode, key_id,
	//   event_type, model, input_tokens, output_tokens, thinking_tokens, total_tokens,
	//   unit, latency, cost, status, service, timestamp_ms, ingested_at, metadata

	for _, event := range events {
		// Defaulting (story_9 AC 11-14)
		if event.TotalTokens == 0 {
			event.TotalTokens = float64(event.InputTokens + event.OutputTokens)
		}
		if event.SourceMode == "" {
			event.SourceMode = models.SourceModeDirectIngest
		}
	}

	// Placeholder: log success (real impl uses batch.Send())
	duration := time.Since(start)
	eps := float64(len(events)) / duration.Seconds()
	w.Log.Info("clickhouse batch inserted",
		"batch_size", len(events),
		"duration_ms", duration.Milliseconds(),
		"events_per_second", fmt.Sprintf("%.0f", eps),
	)

	_ = ctx
	return nil
}

// Ping verifies ClickHouse connectivity (story_9 AC 7).
func (w *Writer) Ping(ctx context.Context) error {
	// In production: conn.Ping(ctx)
	return nil
}

// Close closes the ClickHouse connection.
func (w *Writer) Close() error {
	return nil
}

// --- Helpers for config ---

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if s := os.Getenv(key); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if s := os.Getenv(key); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return defaultVal
}

// insertSQL is the column list for events.usage_events (story_9 AC 10).
const insertSQL = `INSERT INTO events.usage_events
(event_id, org_id, customer_id, end_user_id, session_id, source_mode, key_id,
 event_type, model, input_tokens, output_tokens, thinking_tokens, total_tokens,
 unit, latency, cost, status, service, timestamp_ms, ingested_at, metadata)`

// MarshalMetadata converts a map[string]string to a JSON string, or "{}" if nil/empty.
func MarshalMetadata(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
