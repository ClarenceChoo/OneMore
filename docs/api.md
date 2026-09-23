# API reference

The web client calls the API through `http://127.0.0.1:3000`; direct local access is at `http://127.0.0.1:8080`. Responses use JSON except `GET /api/events`, which is a Server-Sent Events stream. Routes under `/api/` require an `onemore_session` cookie.

## Authentication

For local Compose, `POST /auth/register` accepts `{ "name": "Alex Rivera", "handle": "alex_r", "password": "..." }`. `POST /auth/local/login` accepts a handle and password. Both create a seven-day, HTTP-only, SameSite=Lax session cookie. `POST /auth/logout` clears it. `GET /auth/config` reports whether local and OIDC sign-in are available; `GET /api/me` returns the signed-in user. Local handles contain 3–24 letters, numbers, or underscores; passwords require 10–128 characters when registering.

In production, configure OIDC and use `GET /auth/login` and the configured callback. Local registration is disabled by the production Compose overlay. Cross-site POST requests are rejected using Origin and Fetch Metadata checks. Send `Content-Type: application/json` for JSON POST bodies.

## Plans

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/api/plans` | Chronological page of upcoming plans. Filters: `category`, `area`, `before` (RFC 3339), `open=true`, `cursor`. |
| `POST` | `/api/plans` | Create a plan. An optional `Idempotency-Key` header makes retries return the original plan; returns `201` and the plan. |
| `GET` | `/api/plans/{id}` | Plan detail including live counts and the viewer's status. |
| `GET` | `/api/my-plans` | Hosted and joined plans for the signed-in person. |
| `POST` | `/api/plans/{id}/join` | Confirm a space or enter the waitlist. |
| `POST` | `/api/plans/{id}/leave` | Leave; may advance a waitlisted person. |
| `POST` | `/api/plans/{id}/offer/accept` | Accept the current reserved offer. |
| `POST` | `/api/plans/{id}/offer/decline` | Decline the current offer and advance the queue. |
| `POST` | `/api/plans/{id}/cancel` | Cancel a plan you host. |

Create request:

```http
POST /api/plans HTTP/1.1
Content-Type: application/json
Idempotency-Key: 1d3d2659-c2cb-429e-afde-0e13cb236980

{
  "title": "Badminton doubles after work",
  "description": "Friendly doubles. Bring a racket if you have one.",
  "category": "Badminton",
  "tags": ["casual"],
  "area": "Tiong Bahru",
  "location": "Community sports hall",
  "startsAt": "2026-10-08T10:30:00Z",
  "joinDeadline": "2026-10-08T07:30:00Z",
  "spaces": 2
}
```

Dates are ISO 8601 timestamps with an offset or `Z`; choose future times when actually sending the request. `spaces` counts other participants, excluding the host. A returned plan includes `confirmed`, `reserved`, `waiting`, `available`, `status`, and `myStatus` in addition to its details. `myStatus` can be `host`, `confirmed`, `waitlisted`, `offered`, `left`, or empty. An offered plan includes `offerExpiresAt`.

The feed returns `{ "plans": [...], "nextCursor": "..." }`. Follow `nextCursor` for the next chronological page. All action endpoints return the updated plan.

## Discovery and notifications

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/api/recommendations` | Rule-based **For you** feed; each result may include `recommendationReason`. |
| `GET`, `POST` | `/api/preferences` | Read or save `categories`, `area`, `availability`, and IANA `timezone`. |
| `POST` | `/api/discovery/events` | Record an array of `{ planId, kind, source }` events; returns `204`. |
| `GET` | `/api/notifications` | Persistent Inbox items. |
| `POST` | `/api/notifications/{id}/read` | Mark one notification as read. |
| `GET` | `/api/events` | SSE stream with `refresh` events; refetch current state after an event. |

`availability` is `any`, `weekday_evenings`, or `weekends`. Discovery event `kind` is `impression` or `click` from the client; successful joins are recorded by the server. `source` is `soonest`, `for_you`, or `direct`. See [discovery](discovery.md) for how the ranking and event attribution work.

## Errors and service endpoints

Errors use an HTTP status and a JSON body such as:

```json
{ "code": "unauthorized", "message": "Sign in to continue." }
```

Validation failures use `400`; missing authentication uses `401`; forbidden actions use `403`; missing plans use `404`; conflicting participation or an already used handle can use `409`. Unexpected failures use `500` with a generic message. Clients should use the status and `code`, then show `message` to a person.

`GET /health` checks the database connection. `GET /internal/ops` and `/internal/ops.json` expose local operational measurements on the API port. Keep that port restricted in a deployment; the web proxy does not route `/internal/`.
