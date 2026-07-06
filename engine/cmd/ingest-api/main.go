package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8011"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("ingest-api listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Postgres
	pgOK := checkTCP(os.Getenv("PG_HOST"), os.Getenv("PG_PORT"), "5432")
	// Check Redis
	redisOK := checkTCP(os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT"), "6379")
	// Check Kafka
	kafkaOK := checkTCP(os.Getenv("KAFKA_HOST"), os.Getenv("KAFKA_PORT"), "9092")

	allOK := pgOK && redisOK && kafkaOK
	deps := map[string]string{
		"postgres": boolStatus(pgOK),
		"redis":    boolStatus(redisOK),
		"kafka":    boolStatus(kafkaOK),
	}

	w.Header().Set("Content-Type", "application/json")
	if allOK {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      boolStatus(allOK),
		"dependencies": deps,
	})
}

func checkTCP(envHost, envPort, defaultPort string) bool {
	host := envHost
	if host == "" {
		host = "localhost"
	}
	port := envPort
	if port == "" {
		port = defaultPort
	}

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
