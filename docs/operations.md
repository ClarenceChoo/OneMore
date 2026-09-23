# Operations

## Local dashboard

With the Compose stack running, open [the operations page](http://127.0.0.1:8080/internal/ops). It refreshes every five seconds and reads only local API and PostgreSQL state. The web proxy does not route `/internal/`, and Compose binds the API's host port to `127.0.0.1`. On a Linux host, use an SSH tunnel to inspect it without exposing the API port publicly:

```bash
ssh -L 18080:127.0.0.1:8080 YOUR_HOST
```

Then open `http://127.0.0.1:18080/internal/ops`. The JSON source is `/internal/ops.json` on the same port.

| Measurement | Meaning |
| --- | --- |
| API p95/p99 latency | Approximate histogram bounds for completed `/api/` requests since this API process started; SSE is excluded. |
| API errors | HTTP 5xx count since this process started. HTTP 4xx is shown separately. |
| Database connections | Current acquired, idle, and total connections in this API's pgx pool. |
| Pending deliveries | Outbox rows without `delivered_at`, shared across all workers. |
| Oldest pending | Age of the oldest undelivered outbox row in seconds. |
| Notification delay | p95 age between outbox creation and notification insertion for notifications created in the last 24 hours. |

The dashboard is intentionally small and self-contained. Counters reset when an API process restarts; database measurements persist. Compare p95/p99 only with the traffic mix and time window that produced them. A growing pending count or oldest age while the worker is running is a delivery problem to investigate.

## Database upgrades

SQL files in `internal/core/migrations` are applied in filename order at API startup. A PostgreSQL advisory lock serializes concurrent API startups; each migration runs in a transaction and is recorded in `schema_migrations`. The initial migration is idempotent for databases created before versioned migrations were added. Take a PostgreSQL backup before upgrading a deployed database. Migrations are forward-only; restore from the backup if a rollback is required.

## Failure and recovery exercise

The repeatable process test uses a database ending in `_test` and the configured test Redis endpoint:

```bash
TEST_DATABASE_URL='postgres://onemore:onemore_local@127.0.0.1:55432/onemore_test?sslmode=disable' \
TEST_REDIS_URL='redis://127.0.0.1:6379/14' \
go test ./internal/core -run TestTwoAPIsAndWorkerRecoveryAcrossProcesses -count=1 -v
```

It starts two API processes and two workers, then races two claims for one space. One member is confirmed and one waits. It next points a worker at an unavailable Redis endpoint and verifies the outbox event remains pending. A test-only worker then exits immediately after publishing to Redis but before committing PostgreSQL. Two normal workers recover the event: the Redis stream contains two entries with the same durable event ID, while PostgreSQL contains one notification and one delivered outbox row. The test passed locally on 23 September 2026. It exercises process and delivery boundaries without stopping the user's running Redis container or modifying the app database.
