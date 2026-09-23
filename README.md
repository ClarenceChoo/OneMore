# OneMore

**Make room for one more.** Create a plan, claim an open space, or join the waitlist. When someone leaves, OneMore reserves the space for the next person and sends an offer with a clear expiry.

Built with **Go · PostgreSQL · Redis · React · TypeScript · Docker**.

<p align="center"><img src="docs/media/discover.png" alt="OneMore discovery page showing upcoming plans, filters, and available spaces" width="100%" /></p>

<p align="center"><em>Browse upcoming plans and see availability before opening one.</em></p>

## Contents

- [Try it locally](#try-it-locally)
- [Product tour](#product-tour)
- [How a space moves](#how-a-space-moves)
- [Architecture](#architecture)
- [Development and tests](#development-and-tests)
- [Performance and operations](#performance-and-operations)
- [Documentation](#documentation)

## Try it locally

**Requirement:** Docker with Docker Compose.

```bash
git clone https://github.com/ClarenceChoo/OneMore.git
cd OneMore
docker compose up -d --build
```

Open **[http://127.0.0.1:3000](http://127.0.0.1:3000)** and create a profile. To use several profiles at once, open an Incognito window or another browser profile and create another account. Each browser session signs in independently. No external identity provider is needed for local use.

| Service | Local address |
| --- | --- |
| Web app | `http://127.0.0.1:3000` |
| API and health endpoint | `http://127.0.0.1:8080`, `/health` |
| Operations dashboard | `http://127.0.0.1:8080/internal/ops` |
| PostgreSQL | `127.0.0.1:55432` |
| Redis | `127.0.0.1:6379` |

`docker compose down` stops the stack and keeps data. `docker compose down -v` also removes the local PostgreSQL and Redis volumes.

## Product tour

The main actions stay close to the plans: discover, create, track your plans, and respond in Inbox.

| Find a plan | Open it and claim a space |
| --- | --- |
| <img src="docs/media/for-you.png" alt="For you feed with a reason for a recommended badminton plan" width="100%" /> | <img src="docs/media/plan-detail.png" alt="Plan detail showing time, location, availability, and join button" width="100%" /> |
| **Soonest** has category, time, area, and open-space filters. **For you** ranks plans using saved interests, area, availability, and previous interactions, with a visible reason. | Plan details show the host, meeting place, deadline, remaining spaces, and your current status. |

<details>
<summary>More screens</summary>

| Create a profile | Browse on a phone |
| --- | --- |
| <img src="docs/media/welcome.png" alt="OneMore profile creation screen" width="100%" /> | <img src="docs/media/mobile-discover.png" alt="Responsive mobile discovery screen" width="300" /> |

</details>

These screenshots were captured from the running local app using fictional profiles and plans. The temporary records were removed afterward.

### Watch the product flow

[![Play the 20-second OneMore product tour](docs/media/brag.jpg)](docs/media/brag.mp4)

The clip follows discovery, a join action, and a reserved waitlist offer.

## How a space moves

1. A host creates a plan and sets the number of spaces **for other people**.
2. People join while spaces are open. Later joiners enter an ordered waitlist.
3. When a confirmed person leaves, the first waitlisted person receives a **10-minute reserved offer** in Inbox.
4. Accepting confirms the space. Declining or expiring passes it to the next person.
5. The join deadline closes new participation; the host can cancel the plan.

PostgreSQL transactions lock the plan while changing membership. The capacity rule is **confirmed members + active reserved offers ≤ spaces**. The worker processes expired offers and retries pending notifications after restarts.

## Architecture

```mermaid
flowchart LR
  B[Browser<br/>React + TypeScript] -->|HTTP + session cookie| W[Nginx]
  B -.->|Server-Sent Events| W
  W --> A[Go API]
  A -->|plans, memberships, sessions,<br/>outbox, notifications| P[(PostgreSQL)]
  A <-->|feed ID cache + refresh Pub/Sub| R[(Redis)]
  K[Go worker] -->|offers + pending outbox| P
  K -->|publish refresh / delivery event| R
  R -.->|refresh signal| A
```

**PostgreSQL is the source of truth.** A membership change and its outbox event commit together. The worker publishes pending events to Redis and marks them delivered; an event can be published twice around a crash, so notification persistence deduplicates by the durable event ID. Redis speeds up feed ID lookup and live refresh, but does not decide who gets a space. Browsers refresh current state after an SSE signal, and a periodic SSE tick covers missed signals.

Read the [architecture design](docs/architecture.md) for the data model, join and offer sequence, failure behavior, and design tradeoffs.

## Development and tests

The API and worker entry point is in `cmd/onemore`, domain and persistence code in `internal/core`, versioned SQL in `internal/core/migrations`, and the web app in `web/src`.

For development checks, install Go and Node.js/npm in addition to Docker. Integration tests use a **separate database whose name ends in `_test`** and Redis database 14. Create the database once while Compose is running:

```bash
docker compose exec -T postgres psql -U onemore -d postgres -c 'CREATE DATABASE onemore_test;'
TEST_DATABASE_URL='postgres://onemore:onemore_local@127.0.0.1:55432/onemore_test?sslmode=disable' \
TEST_REDIS_URL='redis://127.0.0.1:6379/14' \
go test -race ./...

cd web
npm ci
npm run build
```

The process recovery test starts two API processes and two workers, races claims for the last space, interrupts delivery after Redis publish, and checks that capacity and notifications remain correct:

```bash
TEST_DATABASE_URL='postgres://onemore:onemore_local@127.0.0.1:55432/onemore_test?sslmode=disable' \
TEST_REDIS_URL='redis://127.0.0.1:6379/14' \
go test ./internal/core -run TestTwoAPIsAndWorkerRecoveryAcrossProcesses -count=1 -v
```

Run the last command from the repository root. See [operations](docs/operations.md) for the recovery exercise and migration notes.

## Performance and operations

The feed originally fetched up to 25 plan details one at a time. It now loads details in one batch and restores feed order and viewer state. In a **local, in-process mixed workload** with 3,000 plans, 12,000 memberships, and 20 clients, throughput rose from **288 to 1,274 requests/s** with the five-second feed ID cache off; browsing p95 fell from **110.5 to 20.7 ms**. The workload mixed browsing, filtering, joins, and leaves. These are measurements of this setup, not deployment capacity guarantees. The [performance study](docs/benchmark.md#mixed-workload-study) includes cache-on results, a reverse-order run, method, limitations, and raw JSON.

The local [operations dashboard](http://127.0.0.1:8080/internal/ops) reports API latency and errors, database connection use, pending outbox events, and notification delay. Counters are per API process and reset on restart. Database migrations are applied at API startup under a PostgreSQL advisory lock.

For a Linux deployment, configure the OIDC variables and a strong database password in `.env` (see `.env.example`), point `DOMAIN` at the host, then run:

```bash
docker compose -f compose.yaml -f compose.prod.yaml up -d --build
```

The production overlay puts Caddy in front for HTTPS, uses secure session cookies, and disables local password registration. Back up PostgreSQL before upgrades. See [operations](docs/operations.md) for details.

## Documentation

| Document | What it covers |
| --- | --- |
| [Product requirements](docs/PRD.md) | Scope, user stories, behavior, and acceptance criteria |
| [Architecture design](docs/architecture.md) | Components, data model, transactional flows, and failure handling |
| [API reference](docs/api.md) | Authentication, endpoints, request examples, and error format |
| [Discovery](docs/discovery.md) | Rule-based ranking, signals, and evaluation approach |
| [Performance study](docs/benchmark.md) | Workload, before/after results, raw data, and caveats |
| [Operations](docs/operations.md) | Dashboard, migrations, and recovery exercise |
| [Implementation plan](docs/implementation-plan.md) | Build sequence and decisions |

**Current scope:** plans, memberships, waitlists, expiring offers, notifications, and explainable discovery. Direct messages, payments, uploaded media, and public profiles are outside this release.
