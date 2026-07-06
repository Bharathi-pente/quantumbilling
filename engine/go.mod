module github.com/pente/quantumbilling/engine

go 1.22

require (
	github.com/lib/pq v1.10.9
	github.com/redis/go-redis/v9 v9.7.0
// TODO: After `go mod tidy` in CI, the following OTel deps will resolve:
// go.opentelemetry.io/otel v1.32.0
// go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.32.0
// go.opentelemetry.io/otel/sdk v1.32.0
// go.opentelemetry.io/otel/trace v1.32.0
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)
