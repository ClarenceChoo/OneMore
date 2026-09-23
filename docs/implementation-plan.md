# OneMore implementation plan

**Goal:** Deliver the standalone first release described in [PRD.md](PRD.md).

**Architecture:** Keep all state transitions in a Go service backed by PostgreSQL transactions. Use a separate worker for durable expiry and outbox delivery. Render the feed, plan detail, creation, and personal views in a small React client.

**Tech stack:** Go, pgx, PostgreSQL, Redis, React, TypeScript, Vite, Docker Compose, GitHub Actions.

## Sequence

1. Initialize the repository, document product requirements, and add local infrastructure.
2. Build the database schema, data access, authentication, and API shell. Commit.
3. Implement transactional creation, discovery, joining, leaving, and host cancellation with tests. Commit.
4. Implement FIFO offers, expiry recovery, notifications, outbox, and SSE with tests. Commit.
5. Build the responsive client with accessible forms and clear participation states. Commit.
6. Add container setup, CI, operational documentation, and verification scripts. Commit.
7. Run focused tests, build checks, and browser review; fix discovered issues. Connect and push the GitHub repository if authentication is available.

## Review focus

- Two people claim the last space concurrently: exactly one becomes confirmed.
- A repeated request or refresh: the same membership is returned without consuming another space.
- An offer expires while being accepted: the final state remains consistent.
- A worker restarts after a database change: pending events and offers still process.
- A client reconnects after missing SSE: it reloads current plan and notification state.
