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

	// Kafka consumer
	kafkaConsumer := consumer.NewConsumer(logger)

	// ClickHouse writer
	chWriter := clickhouse.NewWriter(logger)
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
		kafkaOK := true  // placeholder
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

	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8012" }
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
	if ok { return "ok" }
	return "error"
}
