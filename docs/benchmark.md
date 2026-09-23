# Feed benchmark baseline

Measured on 23 September 2026 with the local Docker Compose stack on an arm64 host. Docker had 10 CPUs and 8.22 GB of memory available. The dataset contained one active plan and three test profiles. The client requested `GET /api/plans` with a valid local session; the five-second shared feed-ID cache was enabled. Twenty concurrent clients ran for ten seconds through Nginx on `127.0.0.1:3000`.

| Metric | Result |
| --- | ---: |
| Requests | 35,025 |
| HTTP 200 responses | 35,025 |
| Errors | 0 |
| Achieved throughput | 3,500.6 requests/s |
| p50 latency | 4.15 ms |
| p95 latency | 14.54 ms |

The load tool reads each response body before reusing the connection. An earlier trial that closed bodies without draining them created transport errors in the load generator; it is excluded from these results. This baseline is synthetic and represents a tiny, cache-friendly dataset. It does not predict throughput with many plans, more filters, or a remote deployment. Repeat the same command with a realistic dataset before tuning indexes or cache behavior.

## Mixed workload study

On the same local arm64 machine, an isolated PostgreSQL database held 3,000 upcoming plans, 12,000 memberships, and 501 users. Twenty clients called an in-process HTTP server for ten seconds per scenario: approximately 70% unfiltered browsing, 20% category and area filtering, 5% joining, and 5% leaving. The load included cookie session lookups. PostgreSQL query calls were counted with pgx's query tracer; the count excludes transaction begin and commit and Redis commands. The Redis scenarios used a separate logical Redis database. All four scenarios returned HTTP 200 with zero transport errors.

| Feed details | Five-second ID cache | Requests/s | Overall p95 / p99 | Browsing p95 / p99 | pgx query calls/request |
| --- | --- | ---: | ---: | ---: | ---: |
| Sequential | Off | 176.5 | 187.5 / 364.1 ms | 183.0 / 383.9 ms | 25.7 |
| Sequential | On | 185.5 | 189.0 / 235.7 ms | 193.2 / 253.4 ms | 24.8 |
| Batched | Off | 778.3 | 63.0 / 95.6 ms | 44.7 / 60.5 ms | 3.9 |
| Batched | On | 840.7 | 56.4 / 83.2 ms | 41.8 / 52.8 ms | 3.0 |

The previous feed queried each of up to 25 plan details in sequence. The batched version loads those details in one query, then restores feed order and each viewer's membership state. Throughput increased about 4.4× without caching and 4.5× with caching in this run. Filtering, join, and leave p95/p99, request counts, and error counts for every scenario are in [the raw JSON results](performance-study-2026-09-23.json). These measurements are from one local run; the in-process HTTP server excludes Nginx and network hop overhead. The changed request mix means read and write percentiles should be read separately from the overall percentile. A repeated study on the target deployment hardware is needed before making capacity claims.

To reproduce, create a dedicated database and run the study. It refuses a database name without the `_bench` suffix and resets only that database:

```bash
docker compose exec -T postgres psql -U onemore -d postgres -c 'CREATE DATABASE onemore_bench;'
STUDY_DATABASE_URL='postgres://onemore:onemore_local@127.0.0.1:55432/onemore_bench?sslmode=disable' \
STUDY_REDIS_URL='redis://127.0.0.1:6379/15' \
go run ./cmd/study -plans 3000 -workers 20 -duration 10s
```

The first command is needed only once. The study compares sequential and batched detail loading with the feed ID cache off and on. The sequential path remains available only to make the comparison reproducible; the application uses batched loading by default.
