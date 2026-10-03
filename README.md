# Paytm Money — Seat Reservation Service

A concurrency-safe seat reservation service built with **Go, Gin, PostgreSQL, Docker, and Prometheus**.

The system is designed to handle high-contention reservation traffic while guaranteeing:

* No double booking
* Atomic multi-seat reservations
* Idempotent retries
* Per-user reservation limits under concurrency
* Token-derived user identity
* Ownership-protected cancellation
* Transactional state consistency
* Graceful shutdown
* Health/readiness checks
* Prometheus metrics
* Structured JSON logging
* Production deployment on Render

---

## Submission / Evaluation

| Requirement                  | Details                                                                                                                             |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Public GitHub repository** | [github.com/Aryan-Jagadale/paytm-seat-reservation](https://github.com/Aryan-Jagadale/paytm-seat-reservation?utm_source=chatgpt.com) |
| **Live deployment**          | [Production API](https://paytm-seat-reservation-aiyr.onrender.com?utm_source=chatgpt.com)                                           |
| **Health check**             | [/health](https://paytm-seat-reservation-aiyr.onrender.com/health?utm_source=chatgpt.com)                                           |
| **Readiness check**          | [/ready](https://paytm-seat-reservation-aiyr.onrender.com/ready?utm_source=chatgpt.com)                                             |
| **Prometheus metrics**       | [/metrics](https://paytm-seat-reservation-aiyr.onrender.com/metrics?utm_source=chatgpt.com)                                         |
| **Burst / acceptance test**  | `go run ./scripts/burst https://paytm-seat-reservation-aiyr.onrender.com`                                                           |
| **Production burst**         | 20,000 concurrent reservation requests                                                                                              |
| **Hot seats**                | 50                                                                                                                                  |
| **Confirmed**                | 50                                                                                                                                  |
| **Seat conflicts**           | 19,950                                                                                                                              |
| **5xx responses**            | 0                                                                                                                                   |
| **Network errors**           | 0                                                                                                                                   |

The repository retains the incremental commit history used during development.

---

# 1. Architecture

```text
                         ┌─────────────────────┐
                         │      Client         │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │    Gin HTTP API     │
                         │                     │
                         │  Request ID         │
                         │  JWT Identity       │
                         │  Structured Logs    │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │ Reservation Service │
                         │                     │
                         │ Validation          │
                         │ Idempotency         │
                         │ Per-user limits     │
                         │ Transaction logic   │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │    PostgreSQL       │
                         │                     │
                         │ Shows               │
                         │ Seats               │
                         │ Reservations        │
                         │ Quota               │
                         │ Idempotency keys    │
                         └─────────────────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │    Prometheus       │
                         │                     │
                         │ Counters            │
                         │ Gauges              │
                         └─────────────────────┘
```

The database is the source of truth for reservation state and concurrency control.

---

# 2. Technology Stack

* **Language:** Go
* **HTTP:** Gin
* **Database:** PostgreSQL
* **Database Driver:** pgx / pgxpool
* **Authentication:** JWT
* **Metrics:** Prometheus
* **Logging:** Go `slog`
* **Containerization:** Docker / Docker Compose
* **Deployment:** Render
* **Testing:** Go testing + custom concurrency acceptance script

---

# 3. Core API

## Create Show

```http
POST /shows
Authorization: Bearer <admin-token>
Content-Type: application/json
```

Example:

```json
{
  "total_seats": 100,
  "price_paise": 10000,
  "per_user_limit": 4
}
```

Returns the created show ID.

---

## Reserve Seats

```http
POST /shows/{showID}/reservations
Authorization: Bearer <user-token>
Content-Type: application/json
Idempotency-Key: <unique-key>
```

Example:

```json
{
  "seat_numbers": ["A1", "A2"]
}
```

Successful reservation:

```text
201 Created
```

Domain conflicts return `409 Conflict`.

Examples:

```text
seat_taken
per_user_limit
idempotent_replay
```

---

## Cancel Reservation

```http
POST /reservations/{reservationID}/cancel
Authorization: Bearer <user-token>
```

A user can only cancel their own reservation.

Attempting to cancel another user's reservation returns:

```text
403 Forbidden
```

---

## Get Shows

```http
GET /shows
```

Returns the current show/seat state.

This endpoint is also used by the acceptance script to continuously reconcile:

```text
available + held + confirmed == total_seats
```

---

# 4. Concurrency Design

The most important part of the system is preventing two concurrent requests from confirming the same seat.

Reservation does not follow:

```text
SELECT seat
    ↓
if available
    ↓
UPDATE seat
```

because two concurrent requests could both observe the seat as available.

Instead, reservation uses an atomic conditional database update inside a transaction:

```sql
UPDATE seats
SET status = 'confirmed',
    reservation_id = $1,
    user_id = $2
WHERE show_id = $3
  AND seat_number = ANY($4)
  AND status = 'available';
```

The application verifies that the expected number of seats were updated.

If fewer seats were updated, the transaction is rolled back and the reservation is rejected.

This makes PostgreSQL responsible for the final concurrency decision.

---

# 5. Atomic Multi-seat Reservations

A reservation containing multiple seats is **all-or-nothing**.

For example:

```text
Request:
[A1, A2, A3]
```

If:

```text
A1 → available
A2 → available
A3 → already confirmed
```

the complete transaction is rolled back.

Result:

```text
No seats are confirmed.
```

This prevents partial reservations.

---

# 6. Idempotency

Clients can safely retry a request using:

```http
Idempotency-Key: abc-123
```

The key is associated with the user, show, requested seats, and resulting reservation.

For the same:

```text
user + show + idempotency key + seats
```

a retry returns the original reservation rather than creating another reservation.

A request that reuses the same idempotency key with different seats is rejected:

```text
409 Conflict
```

Example:

```text
Request 1:
Idempotency-Key: abc
Seats: A1

Request 2:
Idempotency-Key: abc
Seats: A2

→ 409 Conflict
```

This protects the API from duplicate state changes caused by client retries.

---

# 7. Per-user Reservation Limit

Each show has a configurable reservation limit.

Example:

```text
Per-user limit = 4
```

If 10 concurrent requests are made by the same user, the database transaction locks/checks the user's quota row before confirming seats.

Expected result:

```text
4 successful reservations
6 per_user_limit conflicts
```

The quota check is performed inside the same transaction as the seat reservation.

Therefore, concurrent requests cannot bypass the limit through race conditions.

---

# 8. Identity and Authorization

The user identity is derived from the JWT.

The API does not trust a user ID supplied in the request body.

For example, even if a request attempts:

```json
{
  "user_id": "another-user"
}
```

the authenticated identity remains the user encoded in the JWT.

Cancellation also verifies ownership before changing reservation state.

---

# 9. Reservation State

Seats currently support:

```text
available
held
confirmed
```

The current reservation flow primarily uses:

```text
available → confirmed
```

`held` exists in the schema for the reservation lifecycle but does not currently implement an expiry mechanism.

The system continuously verifies the accounting invariant:

```text
available + held + confirmed = total_seats
```

---

# 10. Health and Readiness

## Liveness

```text
GET /health
```

Returns a successful response when the application process is alive.

[Check production health](https://paytm-seat-reservation-aiyr.onrender.com/health?utm_source=chatgpt.com)

---

## Readiness

```text
GET /ready
```

Readiness checks the PostgreSQL connection.

If the database is unavailable, readiness fails closed instead of reporting the service as ready.

[Check production readiness](https://paytm-seat-reservation-aiyr.onrender.com/ready?utm_source=chatgpt.com)

---

# 11. Prometheus Metrics

Metrics are exposed through:

```text
GET /metrics
```

[Production metrics endpoint](https://paytm-seat-reservation-aiyr.onrender.com/metrics?utm_source=chatgpt.com)

The service exposes:

### Confirmed reservations

```text
reservations_confirmed_total
```

Counter of newly confirmed reservations.

### Declined reservations

```text
reservations_declined_total{reason="seat_taken"}
reservations_declined_total{reason="per_user_limit"}
reservations_declined_total{reason="idempotent_replay"}
```

### Available seats

```text
seats_available
```

Current available-seat gauge.

Counters are cumulative for the running application process, while `seats_available` represents current state.

---

# 12. Structured Logging

The application uses Go's structured `slog` logger.

Logs are emitted as JSON and include request/correlation information.

Example:

```json
{
  "time": "2026-10-03T12:52:58Z",
  "level": "INFO",
  "msg": "server starting",
  "address": "0.0.0.0:10000"
}
```

Production logs can be accessed through:

```text
Render Dashboard
→ paytm-seat-reservation
→ Logs
```

Request IDs allow individual requests to be traced through the service.

---

# 13. Production Deployment

The application is deployed on Render using the Dockerfile in this repository.

Production URL:

[https://paytm-seat-reservation-aiyr.onrender.com](https://paytm-seat-reservation-aiyr.onrender.com?utm_source=chatgpt.com)

The deployed service runs the following startup flow:

```text
Application starts
      ↓
Load configuration
      ↓
Create PostgreSQL connection pool
      ↓
Ping database
      ↓
Run migrations
      ↓
Initialize HTTP server
      ↓
Start serving traffic
```

The Docker image uses a multi-stage build:

```text
Go builder image
      ↓
Compile static binary
      ↓
Minimal Alpine runtime image
```

---

# 14. Graceful Shutdown

The HTTP server supports graceful shutdown.

Shutdown sequence:

```text
SIGTERM / SIGINT
      ↓
Stop accepting new requests
      ↓
Allow active requests to finish
      ↓
Close database resources
      ↓
Exit process
```

The shutdown timeout is configurable.

---

# 15. Database Connection Pool

The application uses `pgxpool`.

Default configuration:

```text
MAX_CONNS = 10
MIN_CONNS = 2
DB_CONNECT_TIMEOUT = 5s
SHUTDOWN_TIMEOUT = 10s
```

These values can be configured using environment variables.

---

# 16. Local Development

## Requirements

* Go
* Docker
* Docker Compose

---

## Start PostgreSQL + Application

```bash
docker compose up --build
```

The application is exposed locally on:

```text
http://localhost:8081
```

PostgreSQL is exposed on:

```text
localhost:15432
```

---

# 17. Environment Variables

Required:

```text
DATABASE_URL
AUTH_JWT_SECRET
```

Optional:

```text
PORT
MAX_CONNS
MIN_CONNS
DB_CONNECT_TIMEOUT
SHUTDOWN_TIMEOUT
```

Example:

```text
PORT=8080
MAX_CONNS=10
MIN_CONNS=2
DB_CONNECT_TIMEOUT=5s
SHUTDOWN_TIMEOUT=10s
```

Secrets should not be committed to Git.

---

# 18. Tests

Run the unit/integration test suite:

```bash
go test ./...
```

Run static analysis:

```bash
go vet ./...
```

Both should pass before submission.

---

# 19. Production Acceptance / Burst Test

The repository includes a single acceptance script:

```text
scripts/burst
```

It exercises the main concurrency and correctness requirements.

Run against production:

```bash
go run ./scripts/burst https://paytm-seat-reservation-aiyr.onrender.com
```

The script performs:

1. Admin token generation
2. User token generation
3. 20,000-request hot-seat concurrency test
4. Idempotency test
5. Concurrent per-user limit test
6. Token-derived identity test
7. Cancellation ownership test
8. Final acceptance checks

Configuration used by the production acceptance test:

```text
Burst requests : 20,000
Workers        : 180
Burst users    : 200
Hot seats      : 50
Per-user limit : 4
```

---

# 20. Production Acceptance Result

The following acceptance test was executed against the live Render deployment:

```text
go run .\scripts\burst https://paytm-seat-reservation-aiyr.onrender.com
```

Result:

```text
========================================
       SEAT RESERVATION ACCEPTANCE
========================================

Base URL       : https://paytm-seat-reservation-aiyr.onrender.com
Hot seats      : 50
Burst requests : 20000
Workers        : 180
Burst users    : 200
User limit     : 4

[1/8] Generating admin token...
      PASS

[2/8] Generating user tokens...
      Generated 200 tokens

[3/8] Running hot-seat storm...
      Show ID: d6fb2b5b-6410-44f8-9e14-2903e508be84

========================================
           HOT-SEAT STORM
========================================

Duration       : 6m25.795s
Confirmed      : 50
Declined:
  seat_taken           : 19950
5xx            : 0
Network errors : 0

========================================
      PASS

[4/8] Testing idempotency...
      PASS

[5/8] Testing concurrent per-user limit...
      PASS

[6/8] Testing token-derived identity...
      PASS

[7/8] Testing cancellation ownership...
      PASS

[8/8] Acceptance checks complete

========================================
       ALL ACCEPTANCE TESTS PASSED
========================================
```

### What this demonstrates

For 20,000 concurrent reservation attempts targeting 50 hot seats:

```text
50   → confirmed
19,950 → seat_taken
0    → 5xx
0    → network errors
```

Therefore, every hot seat received only one successful reservation.

The test also verified:

* Idempotent retries
* Same idempotency key with different seats
* Concurrent per-user limits
* JWT-derived identity
* Reservation ownership during cancellation
* State reconciliation

---

# 21. State Reconciliation

During the burst test, the script periodically queries the API and verifies:

```text
available + held + confirmed == total_seats
```

This check is performed during and after the concurrent workload.

The test fails if the invariant is violated.

This provides an application-level check that the database state remains internally consistent under concurrent load.

---

# 22. Example Production Metrics

After the production acceptance run, the metrics endpoint reported:

```text
reservations_confirmed_total 57

reservations_declined_total{reason="idempotent_replay"} 9
reservations_declined_total{reason="per_user_limit"} 6
reservations_declined_total{reason="seat_taken"} 19950

seats_available 8
```

The counters are cumulative across the acceptance scenarios.

For example:

```text
57 confirmed
=
50 hot-seat confirmations
+ 1 idempotency test
+ 4 per-user-limit test
+ 1 identity test
+ 1 cancellation test
```

The `seat_taken` counter corresponds to the 20,000-request hot-seat storm.

---

# 23. Project Structure

```text
.
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── auth/
│   ├── config/
│   ├── db/
│   ├── health/
│   ├── http/
│   ├── metrics/
│   └── reservation/
│
├── migrations/
│   └── 001_init.sql
│
├── scripts/
│   └── burst/
│
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

---

# 24. Docker

Build:

```bash
docker build -t paytm-seat-reservation .
```

Run:

```bash
docker run \
  -p 8080:8080 \
  -e DATABASE_URL="<database-url>" \
  -e AUTH_JWT_SECRET="<jwt-secret>" \
  paytm-seat-reservation
```

For local development, Docker Compose is recommended because it starts PostgreSQL and the application together.

---

# 25. Design Decisions

### Why PostgreSQL?

Reservations require strong transactional guarantees and concurrency control.

PostgreSQL provides:

* Transactions
* Row-level locking
* Conditional updates
* Unique constraints
* Strong consistency

This makes it suitable as the source of truth for seat inventory.

### Why enforce correctness in the database?

Application-level checks such as:

```text
if seat.available {
    reserve()
}
```

are unsafe under concurrent requests.

The database performs the final atomic state transition.

### Why use transactions?

A reservation can involve multiple pieces of state:

```text
Seats
Quota
Idempotency
Reservation
```

These changes must either all succeed or all roll back.

---

# 26. Failure Handling

Expected business conflicts are represented as 4xx responses rather than server errors.

Examples:

```text
400 → invalid request
401 → unauthenticated
403 → unauthorized ownership operation
409 → seat conflict / quota conflict / idempotency conflict
```

Unexpected infrastructure or application failures return 5xx.

The production 20,000-request acceptance test recorded:

```text
5xx = 0
```

---

# 27. Security Considerations

The service currently includes `/dev/token` for self-contained assignment/demo testing.

This endpoint is intended for the assignment environment and should be replaced with a real identity provider in a production system.

Additional production hardening would include:

* External identity provider
* Secret manager
* Key rotation
* Rate limiting
* TLS termination
* Audit logging
* Restricted administrative APIs
* Database credential rotation
* More granular authorization policies

---

# 28. Future Improvements

Possible extensions include:

* Expiring seat holds
* Background hold cleanup
* Redis-based rate limiting
* Distributed tracing
* OpenTelemetry
* Dedicated metrics dashboards
* Horizontal autoscaling
* Read replicas for read-heavy workloads
* External authentication provider
* Stronger administrative authorization
* Load testing with geographically distributed clients

These are intentionally outside the core assignment implementation.

---

# 29. AI Usage

AI assistance was used during development for:

* Reviewing concurrency approaches
* Discussing PostgreSQL transaction semantics
* Debugging and reasoning about Go implementation details
* Reviewing Docker/deployment configuration
* Improving documentation
* Designing acceptance-test scenarios

The core implementation, testing, deployment, and validation were executed against the actual project and live service.

---

# 30. Repository

Source code and commit history:

[GitHub Repository](https://github.com/Aryan-Jagadale/paytm-seat-reservation?utm_source=chatgpt.com)

Production deployment:

[Live API](https://paytm-seat-reservation-aiyr.onrender.com?utm_source=chatgpt.com)

---

## Quick Start

### Local

```bash
docker compose up --build
```

### Tests

```bash
go test ./...
go vet ./...
```

### Production acceptance

```bash
go run ./scripts/burst https://paytm-seat-reservation-aiyr.onrender.com
```

### Production endpoints

```text
GET /health
GET /ready
GET /metrics
```

---

# Final Status

```text
✓ Public GitHub repository
✓ Incremental commit history
✓ Dockerized application
✓ PostgreSQL persistence
✓ Atomic seat reservation
✓ Multi-seat atomicity
✓ Idempotency
✓ Per-user concurrency limit
✓ JWT-derived identity
✓ Cancellation ownership
✓ Health endpoint
✓ Readiness endpoint
✓ Prometheus metrics
✓ Structured JSON logs
✓ Graceful shutdown
✓ Live Render deployment
✓ 20,000-request production burst
✓ 0 production 5xx
✓ 0 production network errors
✓ Acceptance tests passed
```
