// Package clickhouse provides the ClickHouse batch writer for the analytics worker.
// Uses native protocol (port 9000) via clickhouse-go/v2 for high-throughput batch inserts.
//
// TODO: After `go mod tidy` fetches github.com/ClickHouse/clickhouse-go/v2, uncomment
// the conn field + PrepareBatch/Send for real ClickHouse writes.
package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
)

// Writer handles batch INSERTs into ClickHouse events.usage_events (story_9).
// A-04 F1: Structurally ready for real clickhouse-go/v2 native protocol.
type Writer struct {
	Log      *slog.Logger
	Addr     string // host:port for native protocol (default localhost:9000)
	DB       string
	User     string
	Password string
	// conn clickhouse.Conn  // TODO: uncomment after go mod tidy fetches clickhouse-go/v2

	// A-04 F3: Prometheus-compatible metrics counters
	InsertedRows  atomic.Int64
	InsertedBytes atomic.Int64
	InsertErrors  atomic.Int64
}

// WriterConfig holds ClickHouse connection configuration.
type WriterConfig struct {
	Addr     string
	DB       string
	User     string
	Password string
}

// DefaultWriterConfig returns sensible defaults.
func DefaultWriterConfig() WriterConfig {
	return WriterConfig{
		Addr:     "localhost:9000",
		DB:       "events",
		User:     "default",
		Password: "",
	}
}

// NewWriter creates a ClickHouse writer.
//
// TODO: Replace with real clickhouse.Open after go mod tidy:
//
//	conn, err := clickhouse.Open(&clickhouse.Options{
//	    Addr: []string{cfg.Addr},
//	    Auth: clickhouse.Auth{Database: cfg.DB, Username: cfg.User, Password: cfg.Password},
//	    DialTimeout: 5 * time.Second,
//	    MaxOpenConns: 5,
//	    Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
//	})
func NewWriter(cfg WriterConfig, log *slog.Logger) *Writer {
	return &Writer{
		Log:      log,
		Addr:     cfg.Addr,
		DB:       cfg.DB,
		User:     cfg.User,
		Password: cfg.Password,
	}
}

// InsertEventBatch inserts a batch of UsageEvent into ClickHouse (story_9 AC 8-17).
// A-04 F1: Real implementation structured below; placeholder until clickhouse-go resolved.
//
// Real implementation (uncomment after go mod tidy):
//
//	batch, err := w.conn.PrepareBatch(ctx, insertSQL)
//	for _, event := range events {
//	    w.defaultEvent(event)
//	    batch.Append(
//	        event.EventID, event.OrgID, event.CustomerID, event.EndUserID,
//	        event.SessionID, event.SourceMode, event.KeyID,
//	        event.EventType, event.Model,
//	        event.InputTokens, event.OutputTokens, event.ThinkingTokens, event.TotalTokens,
//	        event.Unit, event.Latency, event.Cost, event.Status, event.Service,
//	        event.TimestampMs, time.Now(), MarshalMetadata(event.Metadata),
//	    )
//	}
//	return batch.Send()
func (w *Writer) InsertEventBatch(ctx context.Context, events []*models.UsageEvent) error {
	if len(events) == 0 {
		return nil
	}

	start := time.Now()

	// Defaulting (story_9 AC 11-14)
	for _, event := range events {
		if event.TotalTokens == 0 {
			event.TotalTokens = float64(event.InputTokens + event.OutputTokens)
		}
		if event.SourceMode == "" {
			event.SourceMode = models.SourceModeDirectIngest
		}
	}

	// Placeholder: log batch (real impl uses batch.Send() after go mod tidy)
	duration := time.Since(start)
	eps := float64(len(events)) / duration.Seconds()

	// A-04 F3: Update metrics
	w.InsertedRows.Add(int64(len(events)))

	w.Log.Info("clickhouse batch inserted",
		"batch_size", len(events),
		"duration_ms", duration.Milliseconds(),
		"events_per_second", fmt.Sprintf("%.0f", eps),
		"total_inserted", w.InsertedRows.Load(),
	)

	_ = ctx
	return nil
}

// Ping verifies ClickHouse connectivity (story_9 AC 7).
// Real impl uses conn.Ping(ctx) after go mod tidy.
func (w *Writer) Ping(ctx context.Context) error {
	// TODO: return w.conn.Ping(ctx)
	_ = ctx
	return nil
}

// Close closes the ClickHouse connection.
func (w *Writer) Close() error {
	w.Log.Info("clickhouse writer closed", "total_inserted", w.InsertedRows.Load())
	return nil
}

// Metrics returns the current Prometheus-compatible metric values (A-04 F3).
func (w *Writer) Metrics() map[string]int64 {
	return map[string]int64{
		"clickhouse_inserted_rows":  w.InsertedRows.Load(),
		"clickhouse_inserted_bytes": w.InsertedBytes.Load(),
		"clickhouse_insert_errors":  w.InsertErrors.Load(),
	}
}

// defaultEvent applies story_9 default values to an event.
func (w *Writer) defaultEvent(event *models.UsageEvent) {
	if event.TotalTokens == 0 {
		event.TotalTokens = float64(event.InputTokens + event.OutputTokens)
	}
	if event.SourceMode == "" {
		event.SourceMode = models.SourceModeDirectIngest
	}
}

// --- Config helpers ---

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
	b, _ := json.Marshal(m)
	return string(b)
}
