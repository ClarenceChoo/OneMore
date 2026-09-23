# OneMore

OneMore helps people fill the last few spaces in a plan. Create an activity, claim an open space, or wait for the next one. When someone leaves, the next person receives a reserved offer that expires automatically.

## Run locally

You need Docker and Docker Compose.

```bash
docker compose up -d --build
```

Open [localhost:3000](http://localhost:3000). Choose Alex, Sam, or Jordan to try the local demo. PostgreSQL is available on `127.0.0.1:55432`; Redis is on `127.0.0.1:6379`. The API is on `127.0.0.1:8080`.

Stop the app with `docker compose down`. This keeps database data. To start with an empty database, run `docker compose down -v`.

## Product flow

1. Alex creates a plan with one space.
2. Sam joins and takes the space.
3. Jordan joins the waitlist.
4. Sam leaves. Jordan receives a ten-minute offer in Inbox.
5. Jordan accepts or declines. An expired offer advances to the next person.

Spaces count other participants and exclude the host. A plan closes at its join deadline. Hosts can cancel plans; cancellation invalidates pending offers.

## Architecture

```text
React + TypeScript → Go API → PostgreSQL
                        │
                        └── Redis (feed IDs, refresh events, event stream)
Go worker → expired offers + transactional outbox → notifications
```

PostgreSQL owns all participation state. Join, leave, offer response, cancellation, and expiry lock the plan row before changing allocation. The rule is `confirmed members + active reserved offers <= spaces`. The worker polls durable offers after restarts. Business changes and outbox rows commit together; pending events retry after Redis recovers. Feed IDs are cached for five seconds, while plan details and participation are read from PostgreSQL. SSE tells browsers to refresh; it is safe to miss an update because screens reload current state.

The [PRD](docs/PRD.md) defines the release behavior and acceptance criteria. The [implementation plan](docs/implementation-plan.md) records the build sequence.

## API

The browser uses cookie-based sessions. Main endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/plans` | Upcoming feed with `category`, `area`, `before`, `open`, and `cursor` filters |
| POST | `/api/plans` | Create; accepts `Idempotency-Key` |
| GET | `/api/plans/{id}` | Current detail and your status |
| POST | `/api/plans/{id}/join` | Claim space or join waitlist |
| POST | `/api/plans/{id}/leave` | Leave and advance the queue |
| POST | `/api/plans/{id}/offer/accept` | Accept a reserved space |
| POST | `/api/plans/{id}/offer/decline` | Decline and advance the queue |
| POST | `/api/plans/{id}/cancel` | Host cancellation |
| GET | `/api/my-plans` | Hosted and active memberships |
| GET | `/api/notifications` | Persistent inbox |
| GET | `/api/events` | SSE refresh signal |

Errors are JSON objects with `code` and `message`. The server checks capacity and deadlines using database transactions and server time.

## Checks

The integration tests require a separate database whose name ends in `_test`:

```bash
docker compose exec -T postgres psql -U onemore -d postgres -c 'CREATE DATABASE onemore_test;'
TEST_DATABASE_URL='postgres://onemore:onemore_local@127.0.0.1:55432/onemore_test?sslmode=disable' TEST_REDIS_URL='redis://127.0.0.1:6379' go test -race ./...
cd web && npm ci && npm run build
```

The database creation command only needs to run once. Tests truncate **only** a database named with the `_test` suffix. CI runs the same checks with service containers.

To measure the feed on your own machine, sign in, copy the `onemore_session` cookie value into `ONEMORE_SESSION`, then run:

```bash
go run ./cmd/load -url http://127.0.0.1:3000/api/plans -workers 20 -duration 30s
```

The tool reports request count, errors, throughput, and p50/p95 latency. Record hardware, dataset size, and request mix when sharing results. Synthetic requests are not user activity.

The [local baseline](docs/benchmark.md) records one measured run and its limits.

## Deploy on Linux

Use a host with Docker Compose and a domain pointing to it. Create `.env` from `.env.example`, set a strong `POSTGRES_PASSWORD`, `DOMAIN`, `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `OIDC_REDIRECT_URL` to `https://YOUR_DOMAIN/auth/callback`. Then run:

```bash
docker compose -f compose.yaml -f compose.prod.yaml up -d --build
```

The production overlay disables demo identities, uses secure cookies, and runs Caddy for HTTPS. Configure your OpenID Connect provider to allow the exact callback URL. Back up the PostgreSQL volume. Store `.env` outside version control. Redis can be temporarily unavailable without granting an extra space; notifications and live updates catch up after recovery.

## Current limits

This release does not include direct messages, payments, uploaded media, ranking, or public profiles. The feed is limited to the next 30 days and uses a five-second cache. Database schema changes after the first release need versioned migrations before deployment to an existing database.
