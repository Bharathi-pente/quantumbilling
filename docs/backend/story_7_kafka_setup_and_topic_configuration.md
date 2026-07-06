# Story 7 — Kafka (KRaft) Setup & Topic Configuration

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline / Infrastructure
> **Depends on:** `event-engine-net` network creation
> **Blocks:** Story 4, Story 5, Story 8 (requires broker and topic to exist)

---

## Description

As a **platform operator**, I need Kafka running locally in standalone KRaft mode (without ZooKeeper dependency) so that the ingestion API can publish usage events and the downstream analytics worker/Flink jobs can consume them. The Kafka broker must be integrated with an OpenTelemetry collector for tracing, and have a web interface (Kafka UI) for partition administration and topic inspection.

All required topics (specifically the high-throughput `usage-events` topic with **32 partitions**) must be automatically created on cluster startup.

---

## Acceptance Criteria

### Kafka Standalone Broker (KRaft Mode)

| # | Criterion |
|---|---|
| 1 | Create/maintain `event-engine-kafka/docker-compose.yml` to define the infrastructure stack. |
| 2 | Configure `kafka` service to run Confluent Kafka in KRaft mode (no ZooKeeper) using cluster ID `MkU3OEVBNTcwNTJENDM2Qk`. |
| 3 | Expose broker host port on `9092` (via external environment variable `KAFKA_HOST_PORT`). |
| 4 | Mount persistent volume `kafka_data` at `/var/lib/kafka/data` to preserve cluster state across restarts. |
| 5 | Attach the OpenTelemetry Java Agent to the Kafka container via JVM options (`KAFKA_OPTS`) to auto-instrument broker execution. |
| 6 | Connect all services to the external bridge network `event-engine-net`. |

### Auto-Topic Initialization (`kafka-init`)

| # | Criterion |
|---|---|
| 7 | Define a container `kafka-init` using Confluent Kafka image to run initialization script. |
| 8 | script blocks execution until `kafka` broker is fully healthy (verified via broker API versions check). |
| 9 | Auto-creates `usage-events` topic with **32 partitions** and replication factor of 1 if it does not exist. |
| 10 | Auto-creates `raw-events` topic with **32 partitions** and replication factor of 1. |
| 11 | Auto-creates `analytics-events` and `billing-events` topics with **16 partitions**. |
| 12 | Auto-creates compacted aggregation topics `customer-aggs-1min`, `customer-aggs-5min`, `enduser-aggs-1min`, `enduser-aggs-5min` with `cleanup.policy=compact` and **16/32 partitions**. |
| 13 | Disable broker-side auto-topic creation (`KAFKA_AUTO_CREATE_TOPICS_ENABLE: 'false'`) to prevent clients from auto-creating topics with default settings (e.g. 1 partition) before `kafka-init` completes. Alternatively, configure the ingest API's readiness probe to verify that the `usage-events` topic exists with exactly 32 partitions before reporting healthy. |

### Monitoring & Management UI (`kafka-ui` / `otel-collector`)

| # | Criterion |
|---|---|
| 14 | Run `otel-collector` container mapping OTLP gRPC to `4319` and OTLP HTTP to `4320` ports. |
| 15 | Run `kafka-ui` (Provectus) container exposing web dashboard on port `8085`. |
| 16 | Configure Kafka UI to connect to the internal `kafka:29092` broker address. |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Create Docker network `event-engine-net` and run `docker compose up -d` | All containers (`ee-kafka`, `ee-kafka-init`, `ee-kafka-ui`, `ee-kafka-otel-collector`) start up |
| TC-02 | Kafka startup health check script | API versions query succeeds and reports broker version |
| TC-03 | Topic list verification using Kafka CLI | Topic `usage-events` appears in the list with `32` partitions |
| TC-04 | Access Kafka UI web dashboard | Dashboard loads on `http://localhost:8085`, shows cluster state, broker details, and topics |
| TC-05 | Publish a test message using Kafka console producer | Message is published successfully to partition key matching `org_id` |

---

## Data Tables / Kafka Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| Kafka topic `raw-events` | `CREATE` | Pre-created high-throughput raw ingestion topic |
| Kafka topic `usage-events` | `CREATE` | Pre-created 32-partition usage events topic |
| Kafka topic `analytics-events` | `CREATE` | Downstream analytics event topic |
| Kafka topic `billing-events` | `CREATE` | Downstream billing event topic |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `KAFKA_HOST_PORT` | Host port for Kafka client connections | `9092` |
| `KAFKA_UI_PORT` | Host port for Kafka UI manager | `8085` |
| `KAFKA_ADVERTISED_HOST` | Host address advertised to external clients | `127.0.0.1` |
| `KAFKA_NUM_PARTITIONS` | Default partition count for auto-created topics | `32` |
