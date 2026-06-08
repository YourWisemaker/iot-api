# IoT Device Management Platform

A concurrent IoT device management platform written in Go. It handles device
registration, high-throughput telemetry ingestion, MQTT integration, real-time
status tracking, threshold-based alerting, historical analytics, and live
updates over WebSocket.

## Features

- **Device registration** – full CRUD over devices via REST (register, list,
  fetch, update, delete).
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
- **JWT authentication** – optional bearer-token auth with database-backed
  users (bcrypt password hashing), role-based authorization (`admin`,
  `viewer`), and rotating refresh tokens.

## Architecture

```
           HTTP / WebSocket                MQTT
                   │                          │
         ┌─────────▼─────────┐         ┌──────▼──────┐
         │ api (gorilla/mux) │         │ mqtt bridge │
         │  JWT auth + RBAC  │         └──────┬──────┘
         └─────────┬─────────┘                │
                   │     IngestTelemetry      │
                   └──────────┬───────────────┘
                              ▼
                      ┌───────▼───────┐
                      │    service    │
                      │ (orchestrator)│
                      └───┬───────┬───┘
            Submit(job)   │       │ Broadcast(event)
                      ┌───┘       └────────────────────┐
               ┌──────▼──────┐                ┌────────▼────────┐
               │ worker pool │                │    publisher    │
               └──────┬──────┘                │ local hub /redis│
                      │                        └────────┬────────┘
          ┌───────────┼───────────┐                    │
          ▼           ▼           ▼            ┌───────▼───────┐
     ┌─────────┐ ┌─────────┐ ┌─────────┐       │ realtime hub  │──► WS clients
     │  store  │ │ alerts  │ │  redis  │       └───────────────┘
     │mongo/mem│ │ engine  │ │  cache  │
     └─────────┘ └─────────┘ └─────────┘
```

The store backend (MongoDB or in-memory) holds devices, telemetry, alerts,
users and refresh tokens; user passwords are bcrypt-hashed and refresh tokens
are stored only as SHA-256 hashes.

When Redis is enabled, the service publishes events to a Redis channel instead
of the local hub directly; each instance subscribes and re-broadcasts to its own
WebSocket clients, so real-time events fan out across a horizontally scaled
deployment.

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
| `JWT_TTL` | `15m` | Access-token lifetime |
| `JWT_REFRESH_TTL` | `168h` | Refresh-token lifetime |
| `ADMIN_USERNAME` | `admin` | Bootstrap admin username |
| `ADMIN_PASSWORD` | _(empty)_ | Bootstrap admin password; empty skips seeding |

## Authentication & authorization

Authentication is optional. With `JWT_SECRET` unset the API is open (intended for
local development). When `JWT_SECRET` is set, every route except `/health` and
the `/api/auth/*` endpoints requires a valid `Authorization: Bearer <token>`.

Users are stored in the database with bcrypt-hashed passwords. On startup, an
admin user is seeded from `ADMIN_USERNAME`/`ADMIN_PASSWORD` if it does not
already exist. Two roles are supported: `admin` (full access, including device
writes and user management) and `viewer` (read access plus telemetry ingestion).

Login returns a short-lived access token and a long-lived refresh token. The
refresh token is stored only as a SHA-256 hash and is rotated on every use;
changing a password revokes all of that user's refresh tokens.

```bash
# Log in
TOKENS=$(curl -s -X POST localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<ADMIN_PASSWORD>"}')
ACCESS=$(echo "$TOKENS" | jq -r .access_token)
REFRESH=$(echo "$TOKENS" | jq -r .refresh_token)

# Call a protected endpoint
curl -s localhost:8080/api/devices -H "Authorization: Bearer $ACCESS"

# Rotate the refresh token for a new pair
curl -s -X POST localhost:8080/api/auth/refresh \
  -H 'Content-Type: application/json' \
  -d "{\"refresh_token\":\"$REFRESH\"}"

# Create a viewer user (admin only)
curl -s -X POST localhost:8080/api/users \
  -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
  -d '{"username":"observer","password":"changeme123","roles":["viewer"]}'
```

WebSocket clients (which cannot set custom headers in the browser) may pass the
access token as a query parameter: `ws://localhost:8080/ws?token=<ACCESS>`.

## API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/api/auth/login` | Obtain an access + refresh token pair |
| `POST` | `/api/auth/refresh` | Rotate a refresh token for a new pair |
| `POST` | `/api/auth/logout` | Revoke a refresh token |
| `POST` | `/api/devices` | Register a device (admin) |
| `GET` | `/api/devices` | List devices |
| `GET` | `/api/devices/{id}` | Get a device |
| `PUT` | `/api/devices/{id}` | Update a device (admin) |
| `DELETE` | `/api/devices/{id}` | Delete a device (admin) |
| `POST` | `/api/devices/{id}/telemetry` | Ingest telemetry |
| `GET` | `/api/devices/{id}/telemetry` | Telemetry history (`since`, `limit`) |
| `GET` | `/api/devices/{id}/analytics` | Aggregates (`since`, optional `metric` for series) |
| `GET` | `/api/alerts` | List alerts (optional `device_id`) |
| `POST` | `/api/users` | Create a user (admin) |
| `GET` | `/api/users` | List users (admin) |
| `GET` | `/api/users/{id}` | Get a user (admin) |
| `PUT` | `/api/users/{id}` | Update a user's roles/password (admin) |
| `DELETE` | `/api/users/{id}` | Delete a user (admin) |
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

> When authentication is enabled, the REST examples above also need an
> `Authorization: Bearer <access_token>` header (see Authentication above).

## Alerts

Telemetry is evaluated against threshold rules as it is ingested; a breach
persists an alert and pushes it to WebSocket subscribers. The server ships with
these default rules (configured in `cmd/server/main.go`):

| Metric | Condition | Severity |
|--------|-----------|----------|
| `temperature` | `> 80` | `critical` |
| `battery` | `< 15` | `warning` |
| `humidity` | `> 95` | `warning` |

Retrieve alerts via `GET /api/alerts` (optionally filtered with `?device_id=`).

## WebSocket events

Connecting to `/ws` streams JSON events as they occur. Each event has the shape:

```json
{
  "type": "telemetry",
  "device_id": "b1c2...",
  "payload": { "...": "type-specific" },
  "timestamp": "2026-06-08T12:00:00Z"
}
```

Event `type` is one of:

- `device` – a device was registered or updated (payload: the device)
- `telemetry` – a telemetry sample was ingested (payload: the telemetry)
- `status` – a device's status changed, e.g. reaped to `offline`
- `alert` – an alert rule was breached (payload: the alert)

## Project layout

```
cmd/server            entrypoint, dependency wiring, graceful shutdown
internal/models       domain types
internal/config       environment configuration
internal/store        Store interfaces, in-memory + MongoDB (NoSQL) impls
                      (devices, telemetry, alerts, users, refresh tokens)
internal/cache        Redis status cache + event bus
internal/auth         JWT issuing/verification, users, refresh tokens, RBAC middleware
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
