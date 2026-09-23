# OneMore product requirements

## Product

OneMore helps a small community fill the last few spaces in time-sensitive plans. A host posts an activity, place, start time, join deadline, and number of other people needed. People can claim an open space or join a first-come waitlist. When someone leaves, the next person receives a short-lived offer.

## Audience and success

The first audience is a single community whose members already make small group plans. A successful first release lets a host publish a plan in under a minute, lets another person see and join it in a few taps, and handles a cancellation without manual coordination. Success is measured by completed plans, time to fill, successful offer acceptance, and errors during participation. We will not claim adoption or performance results without measurements.

## Release scope

1. Create a local profile with a unique handle and password, or sign in with a configured OpenID Connect provider when deployed. Separate browser sessions can use different profiles simultaneously.
2. Browse upcoming plans, with filters for category, area, time, and open spaces. Sort by start time and stable ID; paginate with a cursor.
3. Create a plan with a short title and description, category, tags, meeting place, start time, join deadline, and 1–20 spaces. Spaces exclude the host.
4. Join an open plan, or join its FIFO waitlist when full. A user has at most one active membership per plan. Repeated join requests return the current result.
5. Leave a plan. A vacated confirmed space becomes a reserved offer for the earliest eligible waitlisted member. Offers expire after ten minutes or at the join deadline, whichever comes first.
6. Accept or decline an offer. Expired offers cannot be accepted, even before worker cleanup. A declined or expired offer advances the queue.
7. See hosted, confirmed, waiting, and offered plans in My plans. See persistent in-app notifications.
8. Receive live refresh signals through server-sent events. On reconnect, the client reloads authoritative state.
9. Hosts can cancel their plans. Cancellation invalidates active offers and prevents new participation.
10. Let members compare chronological discovery with an explainable For you view. Save interests, preferred area, and local-time availability. Record visible impressions, clicks, and joins with a feed source for later evaluation.
11. Provide a local operations view for API latency and errors, database connections, pending outbox work, and notification delay. Keep it off the public web proxy.

Direct messaging, payments, media uploads, reputation, learned ranking, and public profiles are outside this release.

## UX requirements

The default screen is the upcoming feed. Each item shows what, when, where, remaining spaces, and one clear next action. Filters stay compact and reversible. Members can switch between Soonest and For you and edit discovery preferences in the feed. Recommendations explain their strongest matching signal. The plan detail explains membership status in plain language. Create uses one short form with inline validation. My plans separates hosting, going, waiting, and offers. The interface works at 360 px width, is keyboard operable, and communicates errors without losing form input. Color never carries status alone.

## Rules and state

- `confirmed + active offers <= spaces` for every active plan.
- The database is authoritative. Every allocation mutation locks the invitation row in a transaction.
- Waitlist order uses a database sequence assigned on entry. Leaving and rejoining moves to the back.
- A newly arriving user cannot take a space reserved by an offer.
- A host cannot join their own plan. A cancelled, past, or closed plan cannot be joined.
- An offer's expiry is the earlier of ten minutes after issue and the join deadline.
- An accepted offer makes a member confirmed; a declined or expired offer moves that member off the waitlist and advances the next eligible member.
- Invitation creation accepts an idempotency key. Duplicate keys for the same user return the original creation result.

## System design

React, TypeScript, and Vite render the client. A Go API owns validation, session authentication, REST endpoints, and SSE. PostgreSQL stores all durable state; pgx transactions enforce capacity. Versioned SQL migrations run under a PostgreSQL advisory lock. A separate Go worker expires offers, relays transactionally stored outbox events to Redis Streams, and records notifications idempotently. Redis holds short-lived discovery results and cross-process refresh signals; participation continues correctly if Redis is unavailable. Docker Compose runs the API, worker, PostgreSQL, Redis, and frontend locally. The same containers can run behind HTTPS on a Linux host.

## API

- `GET /api/me`, `GET /api/plans`, `GET /api/plans/{id}`, `GET /api/my-plans`
- `GET /api/recommendations`, `GET /api/preferences`, `POST /api/preferences`, `POST /api/discovery/events`
- `POST /api/plans`, `POST /api/plans/{id}/join`, `POST /api/plans/{id}/leave`, `POST /api/plans/{id}/cancel`
- `POST /api/plans/{id}/offer/accept`, `POST /api/plans/{id}/offer/decline`
- `GET /api/notifications`, `POST /api/notifications/{id}/read`, `GET /api/events`
- `POST /auth/register`, `POST /auth/local/login`, `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout`; local password sign-in is disabled in the production overlay.

Mutation responses contain the updated plan and the caller's status. Errors use a consistent JSON shape with a human-readable message and machine-readable code.

## Reliability and verification

Database constraints enforce foreign keys and one membership per user and plan. Allocation paths use row locks and server time. A polling worker recovers expired offers after restart. Outbox rows are written in the same transaction as membership changes and retried until delivered; notification records deduplicate by event ID. Redis outages may delay live signals but never grant a space. Automated tests cover concurrent claims, repeated joins, FIFO promotion, offer expiry and acceptance races, unauthorized cancellation, and cancellation with an active offer. CI runs Go tests including the race detector, frontend checks and build. A load script records request rate and latency without implying real user activity.

## Release acceptance

The repository starts with documented Compose commands and environment variables. Three people can create separate profiles; one can create a plan, a second can join, a third can wait, and leaving advances the waitlist. The same sequence remains correct across browser refresh and worker restart. All automated checks pass, and the UI is reviewed at desktop and mobile sizes.
