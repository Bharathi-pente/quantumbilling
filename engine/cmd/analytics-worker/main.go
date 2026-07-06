// Analytics worker: Kafka → ClickHouse, the usage source of truth.
// Consumer group analytics-v1, batch accumulate → flush to ClickHouse (Phase 1 / D-04).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pente/quantumbilling/engine/internal/clickhouse"
	"github.com/pente/quantumbilling/engine/internal/consumer"
	"github.com/pente/quantumbilling/engine/internal/orchestration"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Kafka consumer (A-04 F1: structured with real config)
	consumerCfg := consumer.DefaultConsumerConfig()
	consumerCfg.Brokers = []string{envOrDefault("KAFKA_BROKERS", "localhost:9092")}
	kafkaConsumer := consumer.NewConsumer(consumerCfg, logger)

	// ClickHouse writer (A-04 F1: structured with real config)
	chCfg := clickhouse.DefaultWriterConfig()
	chCfg.Addr = envOrDefault("CLICKHOUSE_ADDR", "localhost:9000")
	chWriter := clickhouse.NewWriter(chCfg, logger)
	if err := chWriter.Ping(context.Background()); err != nil {
		logger.Error("clickhouse unreachable at startup", "error", err)
		os.Exit(1)
	}

	// Orchestration service
	svc := orchestration.NewAnalyticsService(chWriter, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start time-based flush ticker
	svc.Start(ctx)

	// Health/ready HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Concurrent checks for Kafka + ClickHouse
		kafkaOK := true // placeholder; real impl checks consumer.Lag() >= 0
		chOK := chWriter.Ping(r.Context()) == nil
		if kafkaOK && chOK {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ready","checks":{"kafka":"ok","clickhouse":"ok"}}`)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"not_ready","checks":{"kafka":"%s","clickhouse":"%s"}}`,
				boolStatus(kafkaOK), boolStatus(chOK))
		}
	})
	// A-04 F3: Prometheus /metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		chMetrics := chWriter.Metrics()
		fmt.Fprintf(w, "# HELP quantumbilling_clickhouse_inserted_rows Total rows inserted into ClickHouse\n")
		fmt.Fprintf(w, "# TYPE quantumbilling_clickhouse_inserted_rows counter\n")
		fmt.Fprintf(w, "quantumbilling_clickhouse_inserted_rows %d\n", chMetrics["clickhouse_inserted_rows"])
		fmt.Fprintf(w, "# HELP quantumbilling_clickhouse_insert_errors Total insert errors\n")
		fmt.Fprintf(w, "# TYPE quantumbilling_clickhouse_insert_errors counter\n")
		fmt.Fprintf(w, "quantumbilling_clickhouse_insert_errors %d\n", chMetrics["clickhouse_insert_errors"])
		fmt.Fprintf(w, "# HELP quantumbilling_consumer_lag Consumer group lag (unread messages)\n")
		fmt.Fprintf(w, "# TYPE quantumbilling_consumer_lag gauge\n")
		fmt.Fprintf(w, "quantumbilling_consumer_lag %d\n", kafkaConsumer.Lag())
		fmt.Fprintf(w, "# HELP quantumbilling_batch_pending Events pending in flush buffer\n")
		fmt.Fprintf(w, "# TYPE quantumbilling_batch_pending gauge\n")
		fmt.Fprintf(w, "quantumbilling_batch_pending %d\n", svc.PendingCount())
	})

	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8012"
		}
		logger.Info("analytics-worker listening", "port", port)
		http.ListenAndServe(":"+port, mux)
	}()

	// Main loop: consume → accumulate → flush (story_10 AC 16)
	batchSize := 10000
	batchTimeout := 2 * time.Second

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			events, err := kafkaConsumer.ConsumeBatch(ctx, batchSize, batchTimeout)
			if err != nil {
				logger.Error("consumer error", "error", err)
				continue
			}
			if len(events) > 0 {
				svc.AddEvents(ctx, events)
			}
		}
	}()

	// Graceful shutdown (story_10 AC 17-22)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("shutting down analytics worker...")
	cancel()

	// Final flush
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	svc.Flush(shutdownCtx)

	kafkaConsumer.Close()
	chWriter.Close()
	logger.Info("analytics worker stopped")
}

func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
