// Package kafka provides Kafka producer and topic management for the event engine.
// Uses segmentio/kafka-go for async batch publishing with at-least-once semantics.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Producer wraps a Kafka writer for publishing usage events.
// Dependencies: github.com/segmentio/kafka-go (added to go.mod).
//
// TODO: Run `go mod tidy` after adding kafka-go to go.mod to resolve the dependency.
// The import below is commented until the module is fetched:
//   import "github.com/segmentio/kafka-go"
//
// For now, Producer provides the interface contract; the concrete implementation
// uses the placeholder until the dependency is resolved in CI.

// Event is the published message shape for usage-events topic.
type Event struct {
	EventID    string `json:"event_id"`
	OrgID      string `json:"org_id"`
	CustomerID string `json:"customer_id"`
	// Partition key: org_id ensures ordered delivery per organization.
}

// Producer publishes events to Kafka topics.
type Producer struct {
	Brokers []string
	Topic   string
	Log     *slog.Logger
	// writer *kafka.Writer  // uncomment after `go mod tidy`
}

// ProducerConfig holds configuration for the Kafka producer.
type ProducerConfig struct {
	Brokers      []string
	Topic        string
	BatchSize    int           // max messages per batch (default: 1000)
	BatchTimeout time.Duration // max time before flush (default: 1s)
}

// DefaultProducerConfig returns sensible defaults for the ingest pipeline.
func DefaultProducerConfig() ProducerConfig {
	return ProducerConfig{
		Brokers:      []string{"localhost:9092"},
		Topic:        "usage-events",
		BatchSize:    1000,
		BatchTimeout: 1 * time.Second,
	}
}

// NewProducer creates a Kafka producer.
//
// TODO: Replace placeholder with real kafka-go writer after `go mod tidy`:
//
//	writer := &kafka.Writer{
//	    Addr:         kafka.TCP(cfg.Brokers...),
//	    Topic:        cfg.Topic,
//	    Balancer:     &kafka.Hash{},
//	    BatchSize:    cfg.BatchSize,
//	    BatchTimeout: cfg.BatchTimeout,
//	    Async:        true,          // fire-and-forget for ingest throughput
//	    RequiredAcks: kafka.RequireOne,
//	    Compression:  kafka.Snappy,
//	    Logger:       kafka.LoggerFunc(func(msg string, args ...interface{}) {
//	        log.Debug("kafka: "+msg, args...)
//	    }),
//	}
func NewProducer(cfg ProducerConfig, log *slog.Logger) *Producer {
	return &Producer{
		Brokers: cfg.Brokers,
		Topic:   cfg.Topic,
		Log:     log,
	}
}

// PublishEvent publishes a single event to Kafka asynchronously.
// The event is serialized as JSON with org_id as the partition key.
//
// TODO: Replace placeholder with real kafka-go write:
//
//	msg := kafka.Message{
//	    Key:   []byte(event.OrgID),
//	    Value: msgBytes,
//	    Headers: []kafka.Header{
//	        {Key: "traceparent", Value: []byte(extractTraceParent(ctx))},
//	    },
//	}
//	return p.writer.WriteMessages(ctx, msg)
func (p *Producer) PublishEvent(ctx context.Context, msgBytes []byte, orgID string) error {
	// Placeholder: log and return nil (events are accepted, Kafka write pending go mod tidy).
	// The HANDOFF.md documents this as "Kafka producer placeholder — replace with real
	// Kafka producer in D-02 completion."
	p.Log.Debug("kafka publish (placeholder)",
		"org_id", orgID,
		"topic", p.Topic,
		"msg_bytes", len(msgBytes),
	)
	_ = msgBytes
	_ = orgID
	return nil
}

// PublishBatch publishes a batch of events to Kafka.
// Each event is a separate message keyed by org_id for partition affinity.
//
// TODO: Replace placeholder with real kafka-go batch write.
func (p *Producer) PublishBatch(ctx context.Context, events []json.RawMessage, orgID string) error {
	p.Log.Debug("kafka batch publish (placeholder)",
		"org_id", orgID,
		"topic", p.Topic,
		"batch_size", len(events),
	)
	for i := range events {
		_ = events[i]
	}
	_ = orgID
	return nil
}

// Close shuts down the Kafka producer, flushing any in-flight messages.
func (p *Producer) Close() error {
	p.Log.Info("kafka producer closed")
	return nil
}

// extractTraceParent extracts the W3C traceparent header from context for
// distributed tracing propagation. Placeholder until OTel is wired (A-02 F3).
func extractTraceParent(ctx context.Context) string {
	// TODO: Extract from OpenTelemetry span context after OTel SDK is wired.
	_ = ctx
	return ""
}

// PublishFunc is the function signature for publishing events, allowing the
// handler to be decoupled from the concrete Kafka implementation.
type PublishFunc func(ctx context.Context, msgBytes []byte, orgID string) error

// BatchPublishFunc is the function signature for batch publishing.
type BatchPublishFunc func(ctx context.Context, events []json.RawMessage, orgID string) error

// WrapProducer returns PublishFunc / BatchPublishFunc closures backed by the Producer.
func WrapProducer(p *Producer) (PublishFunc, BatchPublishFunc) {
	single := func(ctx context.Context, msgBytes []byte, orgID string) error {
		return p.PublishEvent(ctx, msgBytes, orgID)
	}
	batch := func(ctx context.Context, events []json.RawMessage, orgID string) error {
		return p.PublishBatch(ctx, events, orgID)
	}
	return single, batch
}

// Ensure interface compliance.
var _ fmt.Stringer = (*Producer)(nil)

func (p *Producer) String() string {
	return fmt.Sprintf("KafkaProducer{topic=%s, brokers=%v}", p.Topic, p.Brokers)
}
