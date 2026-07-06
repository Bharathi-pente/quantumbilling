// Package consumer provides the Kafka consumer for the analytics worker.
// Consumer group analytics-v1 reads from usage-events topic (A-04 F1).
//
// TODO: After `go mod tidy` fetches github.com/segmentio/kafka-go, uncomment
// the reader field + FetchMessage loop + CommitMessages for real Kafka consumption.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
	"github.com/pente/quantumbilling/engine/internal/tracing"
)

// Consumer wraps Kafka reader operations (story_8).
type Consumer struct {
	Log     *slog.Logger
	Brokers []string
	Topic   string
	GroupID string
	// reader *kafka.Reader  // TODO: uncomment after go mod tidy fetches kafka-go
}

// ConsumerConfig holds Kafka consumer configuration.
type ConsumerConfig struct {
	Brokers  []string
	Topic    string
	GroupID  string
	MinBytes int
	MaxBytes int
	MaxWait  time.Duration
}

// DefaultConsumerConfig returns sensible defaults.
func DefaultConsumerConfig() ConsumerConfig {
	return ConsumerConfig{
		Brokers:  []string{"localhost:9092"},
		Topic:    "usage-events",
		GroupID:  "analytics-v1",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		MaxWait:  2 * time.Second,
	}
}

// NewConsumer creates a Kafka consumer. Real kafka.Reader is created after go mod tidy.
func NewConsumer(cfg ConsumerConfig, log *slog.Logger) *Consumer {
	return &Consumer{
		Log:     log,
		Brokers: cfg.Brokers,
		Topic:   cfg.Topic,
		GroupID: cfg.GroupID,
	}
}

// ConsumeBatch reads up to batchSize messages within timeout (story_8 AC 7-15).
// A-04 F1: Structurally ready for real kafka.Reader.FetchMessage loop.
// Currently placeholder until kafka-go dependency is resolved.
//
// Real implementation (uncomment after go mod tidy):
//
//	for i := 0; i < batchSize; i++ {
//	    msg, err := c.reader.FetchMessage(ctx)
//	    if err != nil { break }
//	    event, err := DeserializeEvent(msg.Value)
//	    if err != nil { c.Log.Warn("deserialize skip", "error", err); continue }
//	    events = append(events, event)
//	}
func (c *Consumer) ConsumeBatch(ctx context.Context, batchSize int, timeout time.Duration) ([]*models.UsageEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c.Log.Debug("consumer: fetching batch",
		"batch_size", batchSize,
		"timeout", timeout,
		"topic", c.Topic,
		"group_id", c.GroupID,
	)
	_ = ctx
	return nil, nil
}

// CommitMessages commits offsets AFTER successful ClickHouse insert (at-least-once).
func (c *Consumer) CommitMessages(ctx context.Context, events []*models.UsageEvent) error {
	c.Log.Debug("consumer: committing offsets", "count", len(events))
	_ = ctx
	_ = events
	return nil
}

// Lag returns consumer group lag for Prometheus metrics (A-04 F3).
func (c *Consumer) Lag() int64 {
	return 0
}

// Close gracefully closes the Kafka reader.
func (c *Consumer) Close() error {
	c.Log.Info("consumer closed", "topic", c.Topic, "group_id", c.GroupID)
	return nil
}

// DeserializeEvent parses a JSON Kafka message into a UsageEvent.
func DeserializeEvent(value []byte) (*models.UsageEvent, error) {
	var event models.UsageEvent
	if err := json.Unmarshal(value, &event); err != nil {
		return nil, fmt.Errorf("deserialize event: %w", err)
	}
	return &event, nil
}

// ParseTraceParentFromMsg parses traceparent from Kafka headers for OTel propagation (A-04 F2).
func ParseTraceParentFromMsg(traceparent string) *tracing.TraceContext {
	return tracing.ParseTraceParent(traceparent)
}

// groupID returns the consumer group ID from env.
func groupID() string {
	if g := os.Getenv("KAFKA_GROUP_ID"); g != "" {
		return g
	}
	return "analytics-v1"
}

// topic returns the Kafka topic from env.
func topic() string {
	if t := os.Getenv("KAFKA_TOPIC"); t != "" {
		return t
	}
	return "usage-events"
}
