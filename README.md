# Seat Reservation Service

A concurrent seat reservation service built with **Go, Gin, PostgreSQL, and pgxpool**.

The service is designed to remain correct under high-concurrency reservation traffic, including multiple users attempting to reserve the same seat simultaneously.

## Key Properties

* No double-selling of seats
* Atomic multi-seat reservations
* Idempotent reservation requests
* Per-user reservation limits
* JWT-based authentication
* Ownership checks for cancellation
* PostgreSQL transactions for correctness
* Deterministic seat ordering to avoid deadlocks
* Prometheus metrics
* Health and readiness endpoints
* Dockerized deployment
* 20,000-request concurrency test

---

## Architecture

```text
                         +-------------------+
                         |      Client       |
                         +---------+---------+
                                   |
                                   v
                         +-------------------+
                         |     Gin API       |
                         +---------+---------+
                                   |
                    +--------------+--------------+
                    |                             |
                    v                             v
             Auth Middleware              Request Middleware
                    |                     Request ID / Logging
                    v
             Reservation Service
                    |
                    v
             PostgreSQL / pgxpool
                    |
       +------------+------------+-------------+
       |            |            |             |
       v            v            v             v
     shows        seats    reservations  show_user_limits
```

PostgreSQL is the authoritative source of truth for seat state.

---

## Tech Stack

* Go 1.27
* Gin
* PostgreSQL
* pgx/v5 + pgxpool
* JWT
* Prometheus
* Docker / Docker Compose

---

## API

### Health

```text
GET /healthz
```

Liveness endpoint.

Example:

```json
{
  "status": "ok"
}
```

### Readiness

```text
GET /readyz
```

Checks the PostgreSQL dependency.

---

### Metrics

```text
GET /metrics
```

Prometheus metrics include:

```text
reservations_confirmed_total
reservations_declined_total
seats_available
```

---

## Authentication

Protected endpoints use:

```text
Authorization: Bearer <JWT>
```

The authenticated user's identity is taken from the JWT.

The client cannot choose another user through the reservation request body.

For local testing, the project includes a development-only token endpoint:

```text
POST /dev/token
```

Request:

```json
{
  "user_id": "user-123",
  "role": "user"
}
```

Admin token:

```json
{
  "user_id": "admin-1",
  "role": "admin"
}
```

> `/dev/token` exists to make the assignment and concurrency scripts self-contained. It should be disabled/replaced by the production identity provider in a real deployment.

---

# API Usage

## 1. Create a Show

Admin-only endpoint:

```text
POST /shows
```

Header:

```text
Authorization: Bearer <admin-token>
Content-Type: application/json
```

Request:

```json
{
  "name": "Concert A",
  "seats": [
    "A1",
    "A2",
    "A3",
    "A4"
  ],
  "price_paise": 50000
}
```

Example response:

```json
{
  "id": "show-id",
  "name": "Concert A",
  "price_paise": 50000
}
```

All requested seats are initially created with:

```text
available
```

---

## 2. Reserve Seats

```text
POST /shows/{show_id}/reserve
```

Requires authentication.

Request:

```json
{
  "seats": [
    "A1",
    "A2"
  ],
  "idempotency_key": "order-123"
}
```

Example successful response:

```json
{
  "reservation_id": "reservation-id",
  "status": "confirmed"
}
```

A successful reservation immediately moves the selected seats:

```text
available -> confirmed
```

---

## 3. Cancel Reservation

```text
POST /reservations/{reservation_id}/cancel
```

Requires authentication.

Only the reservation owner can cancel it.

Successful cancellation:

```text
confirmed -> available
```

---

## 4. Get Show State

```text
GET /shows/{show_id}
```

Returns the show's seats and aggregate counts.

Example:

```json
{
  "id": "show-id",
  "name": "Concert A",
  "total_seats": 4,
  "available": 3,
  "held": 0,
  "confirmed": 1,
  "seats": [
    {
      "seat_number": "A1",
      "status": "confirmed"
    },
    {
      "seat_number": "A2",
      "status": "available"
    }
  ]
}
```

The service maintains:

```text
available + held + confirmed = total_seats
```

---

# Reservation Correctness

## Atomic Seat Claim

The service does not perform a separate read followed by a write.

Instead, it performs:

```sql
UPDATE seats
SET status = 'confirmed',
    user_id = $1
WHERE show_id = $2
  AND seat_number = $3
  AND status = 'available';
```

If multiple requests target the same seat concurrently, PostgreSQL serializes the conflicting row updates.

Exactly one request can change the seat from `available` to `confirmed`.

Other requests observe zero affected rows and receive:

```text
409 Conflict
```

with:

```text
seat_taken
```

This avoids the check-then-act race condition.

---

## Multi-seat Atomicity

A request containing multiple seats is executed inside one PostgreSQL transaction.

Requested seats are sorted deterministically before being processed.

For example:

```text
[A3, A1, A2]
```

becomes:

```text
[A1, A2, A3]
```

This ensures concurrent transactions acquire seats in a consistent order and reduces deadlock risk.

If any requested seat cannot be claimed:

```text
ROLLBACK
```

is performed.

Therefore a request either reserves **all requested seats or none of them**.

---

# Idempotency

Idempotency is backed by PostgreSQL.

The reservation table contains:

```text
show_id
user_id
idempotency_key
```

with the unique constraint:

```sql
UNIQUE (show_id, user_id, idempotency_key)
```

Behavior:

### Same key + same seats

Returns the original reservation.

```text
Request 1 -> 201
Request 2 -> original reservation
Request 3 -> original reservation
```

No additional seat is claimed.

### Same key + different seats

Returns:

```text
409 Conflict
```

This prevents accidental reuse of an idempotency key for a different operation.

---

# Per-user Limit

Each show has a configurable per-user reservation limit.

The default is:

```text
4
```

Concurrency is handled using a row in:

```text
show_user_limits
```

identified by:

```text
(show_id, user_id)
```

The row is locked with:

```sql
SELECT reserved_count
FROM show_user_limits
WHERE show_id = $1
  AND user_id = $2
FOR UPDATE;
```

Therefore concurrent requests from the same user cannot all observe the same reservation count.

For a limit of 4:

```text
10 concurrent requests

4 -> 201
6 -> 409 per_user_limit
```

---

# Holds and Expiry

The schema supports:

```text
available
held
confirmed
```

and includes:

```text
hold_expires_at
```

However, this implementation uses immediate confirmation:

```text
available -> confirmed
```

and explicit cancellation:

```text
confirmed -> available
```

Temporary holds are intentionally not implemented in the current version.

The `held` state and expiry field remain in the schema so a future implementation can support:

```text
available -> held -> confirmed
                 |
                 +-> expired -> available
```

---

# Consistency vs Availability

The reservation path prioritizes consistency and correctness.

PostgreSQL is the authoritative source of truth.

If the database is unavailable, the service does not attempt to confirm seats using local in-memory state.

This means the system may reject reservations during a database/network partition even when seats might technically be available.

The trade-off is intentional:

```text
Prefer:
temporary reservation failure

over:
incorrect seat ownership / double-selling
```

---

# Observability

## Health

```text
GET /healthz
```

Liveness check.

## Readiness

```text
GET /readyz
```

Checks database connectivity.

## Metrics

```text
GET /metrics
```

Example metrics:

```text
reservations_confirmed_total
reservations_declined_total{reason="seat_taken"}
reservations_declined_total{reason="per_user_limit"}
reservations_declined_total{reason="idempotent_replay"}
seats_available
```

Requests also include request IDs and structured access logs.

---

# Running Locally

## Prerequisites

* Go 1.27+
* Docker
* Docker Compose

---

## Start PostgreSQL

The included Docker Compose configuration starts PostgreSQL and the application.

```bash
docker compose up --build
```

The application is exposed on:

```text
http://127.0.0.1:8081
```

---

## Run Tests

Run Go tests:

```bash
go test ./...
```

Optional static checks:

```bash
go vet ./...
```

---

# Concurrency Test Suite

The repository contains dedicated concurrency and correctness test programs.

## 20K Hot-seat Burst

```bash
go run ./scripts/burst http://127.0.0.1:8081
```

This creates a fresh show and sends:

```text
20,000 requests
200 workers
200 users
1 hot seat
```

Expected result:

```text
Confirmed : 1
409       : 19,999
5xx       : 0
```

The test also validates the final reconciliation invariant.

---

## Idempotency Test

```bash
go run ./scripts/idempotency http://127.0.0.1:8081
```

Tests:

* concurrent requests using the same idempotency key
* same reservation returned to all retries
* same key with different seats returns 409

---

## Per-user Limit Test

```bash
go run ./scripts/per_user http://127.0.0.1:8081
```

Runs 10 concurrent reservations for a user with a limit of 4.

Expected:

```text
201 : 4
409 : 6
```

---

## Identity / Spoofing Test

```bash
go run ./scripts/identity http://127.0.0.1:8081
```

Validates:

* JWT identity overrides a spoofed request-body user ID
* another user cannot cancel the reservation
* the reservation owner can cancel it
* the seat returns to available

---

# Test Results

The 20,000-request hot-seat test produced:

```text
Requests       : 20000
Workers        : 200
Users          : 200

Confirmed      : 1
seat_taken     : 19999

5xx            : 0
Network errors : 0
```

Final state:

```text
Total seats    : 1
Available      : 0
Held           : 0
Confirmed      : 1

Reconciliation : PASS
```

Other tests validated:

* idempotent retries
* same-key/different-body conflict
* per-user limit under concurrency
* authentication identity
* cancellation ownership
* all-or-nothing multi-seat reservations

---

# Project Structure

```text
.
├── cmd/
│   ├── server/
│   └── token/
│
├── internal/
│   ├── auth/
│   ├── config/
│   ├── handlers/
│   ├── middleware/
│   ├── metrics/
│   ├── repository/
│   └── service/
│
├── migrations/
│
├── scripts/
│   ├── burst/
│   ├── idempotency/
│   ├── per_user/
│   └── identity/
│
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── README.md
└── WRITEUP.md
```

---

# Production Considerations

For a production deployment, I would additionally:

* replace `/dev/token` with the real identity provider
* implement temporary holds and expiry
* add distributed tracing
* add database lock/latency metrics
* add failure-injection testing
* tune PostgreSQL connection pools
* add rate limiting and abuse protection
* add stronger audit logging
* establish production alerting and dashboards

The core reservation correctness mechanism would continue to rely on the authoritative transactional database.

---

# AI Usage

AI was used as a development aid for:

* reasoning about concurrency and race conditions
* reviewing transaction behavior
* designing concurrency test scenarios
* debugging local Docker/network issues
* improving documentation

The implementation decisions were reviewed and validated against the running service.

The final design and correctness claims are backed by the included concurrency tests, including the 20,000-request hot-seat burst.
