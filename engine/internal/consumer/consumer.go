// Package consumer provides the Kafka consumer for the analytics worker.
// Consumer group analytics-v1 reads from usage-events topic.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pente/quantumbilling/engine/internal/models"
)

// Consumer wraps Kafka reader operations (story_8).
// In production: uses github.com/segmentio/kafka-go.
type Consumer struct {
	Log *slog.Logger
	// In production: *kafka.Reader
}

// NewConsumer creates a Kafka consumer for group analytics-v1 on usage-events.
func NewConsumer(log *slog.Logger) *Consumer {
	return &Consumer{Log: log}
}

// ConsumeBatch reads up to batchSize messages within timeout (story_8 AC 7-15).
func (c *Consumer) ConsumeBatch(ctx context.Context, batchSize int, timeout time.Duration) ([]*models.UsageEvent, error) {
	// In production: reader.FetchMessage(ctx) loop
	// For now: placeholder returning empty slice
	c.Log.Debug("consumer: fetching batch", "batch_size", batchSize, "timeout", timeout)

	// Placeholder: simulate empty topic
	_ = ctx
	return nil, nil
}

// CommitMessages commits offsets for the consumed messages.
func (c *Consumer) CommitMessages(ctx context.Context, events []*models.UsageEvent) error {
	// In production: reader.CommitMessages(ctx, kafkaMessages...)
	return nil
}

// Close gracefully closes the Kafka reader.
func (c *Consumer) Close() error {
	return nil
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

// DeserializeEvent parses a JSON Kafka message into a UsageEvent.
func DeserializeEvent(value []byte) (*models.UsageEvent, error) {
	var event models.UsageEvent
	if err := json.Unmarshal(value, &event); err != nil {
		return nil, fmt.Errorf("deserialize event: %w", err)
	}
	return &event, nil
}

// ExtractTraceParent extracts the W3C traceparent header from Kafka message headers.
func ExtractTraceParent(headers map[string]string) string {
	if tp, ok := headers["traceparent"]; ok {
		return tp
	}
	return ""
}
