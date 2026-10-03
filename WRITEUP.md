# Seat Reservation Service — Technical Writeup

## 1. Atomic Decision

The core correctness requirement is preventing two concurrent users from confirming the same seat.

The exact mechanism used is a **conditional PostgreSQL `UPDATE` inside a transaction**, where the seat can only be changed from `available` to `confirmed`:

```sql
UPDATE seats
SET status = 'confirmed',
    user_id = $1
WHERE show_id = $2
  AND seat_number = $3
  AND status = 'available';
```

The application does **not** first read the seat and then update it. This avoids the classic check-then-act race:

```text
SELECT seat -> available
        |
        +--> Request A
        |
        +--> Request B
```

Instead, the availability check and state transition happen as one database operation.

PostgreSQL takes a row-level lock when updating the matching seat. If two requests concurrently attempt to claim the same seat, only one transaction can successfully change the row from `available` to `confirmed`.

The result is:

```text
Request A -> UPDATE ... WHERE status='available' -> 1 row affected -> success

Request B -> UPDATE ... WHERE status='available' -> 0 rows affected -> 409 seat_taken
```

The application treats `RowsAffected() == 0` as a normal domain conflict rather than an internal server error.

This makes the hot-seat operation race-free without requiring an application-level mutex, which would not work reliably across multiple service instances.

### Multi-seat reservations and deadlocks

A reservation can contain multiple seats. All seat updates and reservation records are performed inside the same PostgreSQL transaction.

Before attempting the updates, the requested seat numbers are sorted deterministically.

For example:

```text
Request A: [A3, A1, A2]
Request B: [A2, A3, A1]

Both become:

[A1, A2, A3]
```

Therefore concurrent transactions acquire/update seats in the same deterministic order.

This avoids the common deadlock pattern:

```text
Transaction A locks A1 -> waits for A2
Transaction B locks A2 -> waits for A1
```

Because both transactions use the same ordering, they do not acquire the same set of rows in conflicting orders.

If any requested seat cannot be claimed, the entire transaction is rolled back. Therefore a multi-seat reservation is all-or-nothing.

---

## 2. Idempotency

The idempotency key is stored directly on the `reservations` table:

```text
show_id
user_id
idempotency_key
```

The database enforces uniqueness with:

```sql
UNIQUE (show_id, user_id, idempotency_key)
```

The application therefore does not rely on an in-memory map for idempotency. The database remains the source of truth and the mechanism works correctly across multiple application instances.

The intended behavior is:

```text
Same user + same show + same idempotency key
        |
        +--> existing reservation found
        |
        +--> return the original reservation
```

A retry does not create another reservation or claim another seat.

For the same key with a different request body, the service compares the requested seats with the original reservation.

Therefore:

```text
Same key + same seats
    -> return original reservation

Same key + different seats
    -> 409 conflict
```

This prevents a client from accidentally reusing an idempotency key for a different operation.

The uniqueness constraint also provides protection against two concurrent requests arriving with the same idempotency key.

---

## 3. Per-user Reservation Limit

The per-user limit is enforced using the `show_user_limits` table:

```text
(show_id, user_id) -> reserved_count
```

For concurrent requests from the same user, the quota row is locked using:

```sql
SELECT reserved_count
FROM show_user_limits
WHERE show_id = $1
  AND user_id = $2
FOR UPDATE;
```

The check and increment happen inside the same transaction as the seat reservation.

For example, with a limit of 4:

```text
Request 1 -> 0 -> 1
Request 2 -> 1 -> 2
Request 3 -> 2 -> 3
Request 4 -> 3 -> 4
Request 5 -> rejected
```

Because concurrent requests serialize on the same quota row, multiple requests cannot all observe the same old count and exceed the limit.

If a later seat claim fails, the transaction rolls back the quota update as well.

---

## 4. Holds and Expiry

The database schema supports the following seat states:

```text
available
held
confirmed
```

and contains:

```text
hold_expires_at
```

to support temporary holds.

However, this implementation intentionally uses **immediate confirmation** rather than an intermediate hold:

```text
available -> confirmed
```

A successful reservation therefore does not occupy the `held` state.

Cancellation transitions the seats back:

```text
confirmed -> available
```

The `held` state and `hold_expires_at` fields are retained in the schema so that a future implementation can support:

```text
available
    |
    v
held
    |
    | expiry
    v
available

held
    |
    | payment success
    v
confirmed
```

If temporary holds are added, expiry should be handled by a background worker or scheduled job using a transactional conditional update, for example only expiring holds whose `hold_expires_at` has actually passed.

The current implementation does not pretend to have an expiry mechanism that it does not implement.

---

## 5. Consistency vs Availability Under a Partition

This service deliberately prioritizes **consistency and correctness over availability** for reservation writes.

PostgreSQL is the source of truth for seat ownership.

If the application cannot reach the database, it will not attempt to confirm a reservation using local in-memory state.

The service therefore prefers:

```text
Database unavailable
        |
        v
reservation fails
```

rather than:

```text
Database unavailable
        |
        v
assume seat is available
        |
        v
confirm locally
        |
        v
possible double-sale
```

During a database/network partition, the system may temporarily reject reservations even if a seat might physically be available.

That is an intentional trade-off because a false negative reservation is preferable to confirming the same seat to two users.

The important invariant is:

```text
available + held + confirmed = total seats
```

State-changing operations are performed transactionally so that the service does not intentionally expose partially committed reservation state.

---

## 6. Observability

The service exposes:

```text
GET /healthz
GET /readyz
GET /metrics
```

### Health checks

`/healthz` is the liveness endpoint.

`/readyz` verifies that the database dependency is available before considering the service ready to receive traffic.

### Metrics

The service exposes Prometheus metrics including:

```text
reservations_confirmed_total
reservations_declined_total{reason="seat_taken"}
reservations_declined_total{reason="per_user_limit"}
reservations_declined_total{reason="idempotent_replay"}
```

The service also exposes the current available-seat count.

Request IDs and structured access logs allow an individual reservation request to be traced through the API.

### What I would page for at 2am

I would page on signals that indicate either correctness risk or a significant service outage.

Examples:

1. **Any detected double-sell**

   * Highest severity.
   * Indicates a correctness failure.

2. **5xx error rate above the expected baseline**

   * Indicates service/database/application failure.

3. **Database readiness failures**

   * Indicates that reservation writes may be unavailable.

4. **Reservation latency degradation**

   * Particularly high p95/p99 latency during traffic bursts.

5. **Unexpected increase in `seat_taken` conflicts**

   * A sustained abnormal increase could indicate traffic behavior or application problems and should be investigated.

6. **Invariant/reconciliation failure**

   * Any case where:

   ```text
   available + held + confirmed != total
   ```

   should be treated as a correctness incident.

7. **Abnormal idempotency or per-user-limit errors**

   * A sudden unexpected increase can indicate client retry storms, abuse, or an application regression.

I would distinguish between **page-worthy incidents** and metrics that are useful for dashboards/debugging. For example, a normal increase in `seat_taken` during a popular event is not itself an incident.

---

## 7. Concurrency Validation

The hardest concurrency scenario was tested with 20,000 concurrent reservation requests targeting the same seat.

The test produced:

```text
Requests       : 20000
Workers        : 200
Users          : 200

Confirmed      : 1
seat_taken     : 19999
5xx            : 0
Network errors : 0
```

Final reconciliation:

```text
Total seats    : 1
Available      : 0
Held           : 0
Confirmed      : 1

0 + 0 + 1 = 1
```

Additional tests validated:

* same-key concurrent idempotency
* same-key/different-seats conflict
* concurrent per-user limit enforcement
* token identity versus spoofed request identity
* reservation ownership during cancellation
* all-or-nothing multi-seat reservation

---

## 8. AI Usage — Directed vs Decided

AI assistance was used as a development aid, but the final design decisions were made based on the assignment requirements and verification against the running implementation.

### Directed by me

I explicitly directed the development toward:

* PostgreSQL as the source of truth
* Go/Gin implementation
* transactional seat reservation
* prevention of double-selling under concurrent requests
* idempotency
* per-user reservation limits
* token-derived identity
* explicit cancellation
* concurrency/burst testing
* Docker-based deployment

I also directed the testing scenarios, including the 20,000-request hot-seat burst.

### Assisted by AI

AI was used to help:

* reason through race conditions and transaction behavior
* explain PostgreSQL row-level concurrency
* review potential deadlock scenarios
* structure concurrency test programs
* debug local Docker/network issues
* improve error handling and observability
* draft documentation and explain the implementation clearly

### Decided and verified by me

The important implementation choices were reviewed and tested locally, including:

* conditional seat `UPDATE`
* deterministic ordering of multi-seat requests
* `FOR UPDATE` for per-user quota serialization
* database uniqueness for idempotency
* JWT-derived user identity
* cancellation ownership checks
* all-or-nothing transactions
* the 20,000-request burst test

The final concurrency behavior was validated against the running service rather than relying only on AI-generated reasoning.

---

## 9. What I Would Do Next

If this were being taken beyond the assignment, I would prioritize the following.

### 1. Implement real temporary holds and expiry

Add:

```text
available -> held -> confirmed
                 |
                 +-> expired -> available
```

with a background expiry worker and transactional state transitions.

### 2. Add database constraints for stronger invariants

Where practical, move more correctness guarantees into PostgreSQL constraints rather than relying only on application logic.

### 2. Add more failure-injection tests

I would test:

* database connection failures
* transaction rollbacks
* application crashes during reservation
* client timeout followed by retry
* d3tabase failover
* concurrent cancellation
* concurrent hold expiry
* network interruption during a request

The important property would be that an uncertain client outcome can always be safely retried using the idempotency key.

### 4. Improve production observability

I would add:

* distributed tracing
* database latency metrics
* transaction/lock wait metrics
* p95/p99 reservation latency
* per-show contention metrics
* structured error codes
* dashboards and alert thresholds

### 5. Scale the database path

At significantly higher scale, I would investigate:

* PostgreSQL connection-pool sizing
* query/index tuning
* partitioning strategies if the seat/reservation tables become very large
* read replicas for non-critical show queries
* separating read-heavy show availability traffic from reservation writes

Reservation writes would continue to use the authoritative transactional database path.

### 6. Security hardening

The development token endpoint would be disabled in production and replaced by the real authentication/identity provider.

I would also add:

* stricter JWT key management
* rate limiting
* request-size limits
* audit logging
* secret management
* authorization tests
* abuse protection around reservation attempts

---

## 10. Summary

The core correctness decision is to make PostgreSQL perform the seat availability check and state transition atomically through a conditional `UPDATE`.

Combined with:

* deterministic multi-seat ordering
* PostgreSQL transactions
* row-level quota locking
* database-backed idempotency
* token-derived identity
* transactional rollback
* explicit cancellation

the service can safely handle concurrent attempts against the same seats without double-selling.

The implementation intentionally favors consistency over availability when the authoritative database is unavailable, because reservation correctness is more important than accepting an uncertain write.

The concurrency tests demonstrate the intended behavior under a 20,000-request hot-seat burst with zero 5xx responses and exactly one successful reservation.
