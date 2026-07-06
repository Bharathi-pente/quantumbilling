package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pente/quantumbilling/engine/internal/auth"
	"github.com/pente/quantumbilling/engine/internal/daemon"
	"github.com/pente/quantumbilling/engine/internal/handler"
	"github.com/pente/quantumbilling/engine/internal/kafka"
	"github.com/redis/go-redis/v9"

	_ "github.com/lib/pq"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	port := envOrDefault("PORT", "8011")

	redisAddr := envOrDefault("REDIS_ADDR", "localhost:6379")
	rdb := redis.NewClient(&redis.Options{
		Addr:        redisAddr,
		Password:    os.Getenv("REDIS_PASSWORD"),
		DB:          0,
		DialTimeout: 2 * time.Second,
	})

	pgDSN := envOrDefault("DATABASE_URL", "postgresql://quantum:quantum-dev-password@localhost:5432/quantumbilling?sslmode=disable")
	pg, err := sql.Open("postgres", pgDSN)
	if err != nil {
		logger.Warn("postgres not available", "error", err)
		pg = nil
	} else {
		pg.SetMaxOpenConns(5)
		pg.SetMaxIdleConns(2)
	}

	// Kafka producer (A-02 F1: real producer, placeholder until go mod tidy + kafka-go dep)
	kafkaCfg := kafka.DefaultProducerConfig()
	kafkaCfg.Brokers = []string{envOrDefault("KAFKA_BROKERS", "localhost:9092")}
	kafkaProducer := kafka.NewProducer(kafkaCfg, logger)
	singlePub, batchPub := kafka.WrapProducer(kafkaProducer)

	ingestHandler := handler.NewIngestHandler(rdb, pg, logger, singlePub, batchPub)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)

	ingestMux := http.NewServeMux()
	ingestMux.HandleFunc("/v1/events", ingestHandler.HandleSingleEvent)
	ingestMux.HandleFunc("/v1/events/batch", ingestHandler.HandleBatchEvent)
	mux.Handle("/v1/", auth.AuthMiddleware(rdb, logger)(ingestMux))

	// Start cache sync daemon (story_3)
	if pg != nil {
		cacheDaemon := daemon.New(pg, rdb, logger)
		cacheDaemon.Start(context.Background())
		logger.Info("cache daemon started", "sync_interval", cacheDaemon.Interval)
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	logger.Info("ingest-api listening", "port", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	pgOK := checkTCP(envOrDefault("PG_HOST", "localhost"), envOrDefault("PG_PORT", "5432"))
	redisOK := checkTCP(envOrDefault("REDIS_HOST", "localhost"), envOrDefault("REDIS_PORT", "6379"))
	kafkaOK := checkTCP(envOrDefault("KAFKA_HOST", "localhost"), envOrDefault("KAFKA_PORT", "9092"))

	allOK := pgOK && redisOK && kafkaOK
	status := http.StatusOK
	if !allOK {
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"status":"%s","dependencies":{"postgres":"%s","redis":"%s","kafka":"%s"}}`,
		boolStatus(allOK), boolStatus(pgOK), boolStatus(redisOK), boolStatus(kafkaOK))
}

func checkTCP(host, port string) bool {
	conn, err := net.DialTimeout("tcp", host+":"+port, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "unavailable"
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
