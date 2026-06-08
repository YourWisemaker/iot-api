# IoT Device Management Platform

A concurrent IoT device management platform written in Go. It handles device
registration, high-throughput telemetry ingestion, MQTT integration, real-time
status tracking, threshold-based alerting, historical analytics, and live
updates over WebSocket.

## Features

- **Device registration** – register, list, fetch and delete devices via REST.
- **Telemetry ingestion** – ingest metrics over HTTP or MQTT, processed
  asynchronously by a bounded worker pool with backpressure.
- **MQTT integration** – devices publish to `devices/<id>/telemetry`; the
  bridge forwards messages into the ingestion pipeline.
- **Real-time status** – devices are marked `online` on telemetry and reaped to
  `offline` by a background reconciler.
- **Alert notifications** – configurable threshold rules raise alerts that are
  persisted and pushed to subscribers.
- **Historical analytics** – per-metric aggregates (min/max/avg/sum/last) and
  ordered time series over a query window.
- **WebSocket support** – clients subscribe to a live event stream (telemetry,
  status, alerts).
- **Concurrency** – a worker pool processes ingestion concurrently; all shared
  state is guarded and verified with `go test -race`.
- **NoSQL persistence** – MongoDB-backed store (falls back to in-memory).
- **Redis** – device status cache plus a pub/sub event bus for fanning
  real-time events across multiple API instances.
- **JWT authentication** – optional bearer-token auth protecting the REST API
  and WebSocket stream, with a credential-based login endpoint.

## Architecture

```
            HTTP / WebSocket                 MQTT
                  │                            │
            ┌─────▼─────┐                ┌─────▼─────┐
            │  api      │                │  mqtt     │
            │ handlers  │                │  bridge   │
            └─────┬─────┘                └─────┬─────┘
                  │        IngestTelemetry     │
                  └───────────────┬────────────┘
                                  ▼
                          ┌───────────────┐
                          │   service     │
                          │ (orchestrator)│
                          └───┬───────┬───┘
            Submit(job)       │       │      Broadcast(event)
                 ┌────────────▼─┐   ┌─▼───────────────┐
                 │ worker pool  │   │ publisher       │
                 └──────┬───────┘   │ hub │ redis bus │
                        │           └─────┬───────────┘
        ┌───────────────┼──────────┐     │
        ▼               ▼          ▼     ▼
   ┌─────────┐    ┌──────────┐  ┌──────────┐
   │  store  │    │  alerts  │  │ realtime │ ──► WebSocket clients
   │ mongo / │    │  engine  │  │   hub    │
   │ memory  │    └──────────┘  └──────────┘
   └─────────┘
```

## Getting started

### Prerequisites

- Go 1.24+
- (Optional) Docker, to run MongoDB, Redis and an MQTT broker

### Run the dependencies

```bash
docker compose up -d
```

### Run the server

```bash
cp .env.example .env   # then export the values, or set env vars directly
go run ./cmd/server
```

With no `MONGO_URI` the server uses an in-memory store, and with no `REDIS_ADDR`
events are broadcast locally only. Both are optional, so the server runs with
zero external dependencies out of the box:

```bash
go run ./cmd/server
```

## Configuration

All configuration is via environment variables (see `.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `WORKERS` | `8` | Worker pool size |
| `QUEUE_SIZE` | `1024` | Ingestion queue capacity |
| `MAX_HISTORY` | `1000` | Telemetry points retained per device |
| `OFFLINE_AFTER` | `30s` | Mark device offline after this idle time |
| `RECONCILE_EVERY` | `10s` | Status reconciliation interval |
| `MONGO_URI` | _(empty)_ | MongoDB URI; empty uses in-memory store |
| `MONGO_DB` | `iot` | MongoDB database name |
| `REDIS_ADDR` | _(empty)_ | Redis address; empty disables Redis |
| `REDIS_PASSWORD` | _(empty)_ | Redis password |
| `REDIS_DB` | `0` | Redis logical database |
| `REDIS_STATUS_TTL` | `5m` | TTL for cached device status |
| `MQTT_BROKER_URL` | _(empty)_ | MQTT broker URL; empty disables MQTT |
| `MQTT_TOPIC_PREFIX` | `devices` | MQTT topic prefix |
| `JWT_SECRET` | _(empty)_ | HMAC secret; empty disables authentication |
| `JWT_TTL` | `1h` | Issued token lifetime |
| `AUTH_USERNAME` | `admin` | Login username |
| `AUTH_PASSWORD` | _(empty)_ | Login password; empty disables the login endpoint |

## Authentication

Authentication is optional. With `JWT_SECRET` unset the API is open (intended for
local development). When `JWT_SECRET` is set, every route except `/health` and
`/api/auth/login` requires a valid `Authorization: Bearer <token>` header.

```bash
# Obtain a token
TOKEN=$(curl -s -X POST localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<AUTH_PASSWORD>"}' | jq -r .token)

# Call a protected endpoint
curl -s localhost:8080/api/devices -H "Authorization: Bearer $TOKEN"
```

WebSocket clients (which cannot set custom headers in the browser) may pass the
token as a query parameter: `ws://localhost:8080/ws?token=<TOKEN>`.

## API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/api/auth/login` | Obtain a JWT (when auth is enabled) |
| `POST` | `/api/devices` | Register a device |
| `GET` | `/api/devices` | List devices |
| `GET` | `/api/devices/{id}` | Get a device |
| `DELETE` | `/api/devices/{id}` | Delete a device |
| `POST` | `/api/devices/{id}/telemetry` | Ingest telemetry |
| `GET` | `/api/devices/{id}/telemetry` | Telemetry history (`since`, `limit`) |
| `GET` | `/api/devices/{id}/analytics` | Aggregates (`since`, optional `metric` for series) |
| `GET` | `/api/alerts` | List alerts (optional `device_id`) |
| `GET` | `/ws` | WebSocket event stream |

### Examples

```bash
# Register a device
curl -s -X POST localhost:8080/api/devices \
  -H 'Content-Type: application/json' \
  -d '{"name":"thermostat-1","type":"sensor","location":"lab"}'

# Ingest telemetry (use the returned device id)
curl -s -X POST localhost:8080/api/devices/<id>/telemetry \
  -H 'Content-Type: application/json' \
  -d '{"metrics":{"temperature":85,"battery":42}}'

# Query analytics with a time series for one metric
curl -s "localhost:8080/api/devices/<id>/analytics?since=1h&metric=temperature"

# Subscribe to the live event stream
websocat ws://localhost:8080/ws
```

### MQTT

Publish telemetry to `devices/<id>/telemetry`:

```bash
mosquitto_pub -t devices/<id>/telemetry \
  -m '{"metrics":{"temperature":85}}'
```

## Project layout

```
cmd/server            entrypoint, dependency wiring, graceful shutdown
internal/models       domain types
internal/config       environment configuration
internal/store        Store interface, in-memory + MongoDB (NoSQL) impls
internal/cache        Redis status cache + event bus
internal/auth         JWT issuing/verification + HTTP middleware
internal/worker       bounded worker pool
internal/alerts       threshold rule engine
internal/analytics    aggregation + time series
internal/realtime     WebSocket pub/sub hub
internal/mqtt         MQTT bridge
internal/service      orchestration of the above
internal/api          HTTP + WebSocket handlers
```

## Testing

```bash
# Unit tests (hermetic; MongoDB integration tests are skipped)
go test ./...

# With the race detector
go test -race ./...

# Include MongoDB integration tests
MONGO_TEST_URI=mongodb://localhost:27017 go test ./internal/store/...
```

Redis behavior is tested in-process with `miniredis`, so no Redis server is
required for the default test run.
