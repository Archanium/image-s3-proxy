# Spec: Bearer-token auth on POST /_/worker/trigger

**Track ID:** add-worker-trigger-auth
**Type:** quick

## What

Add optional `Authorization: Bearer <token>` check to `POST /_/worker/trigger`. Token comes from a new `WORKER_AUTH_TOKEN` env var. When the env var is **unset or empty**, the check is **disabled** (backwards-compatible). When set, the trigger endpoint requires the header AND a constant-time-equal token; otherwise responds `401 Unauthorized` with a `WWW-Authenticate: Bearer realm="worker-trigger"` header.

Closes `draft/architecture.md §9 gap #5` (no auth on the worker trigger; risk made more concrete by PR #4 which removed the clientId=13 limit on impersonation surface).

## Acceptance Criteria

- [ ] **AC1.** Reading `WORKER_AUTH_TOKEN` from env is done only in `cmd/image-proxy/main.go` (per the project's "env reads only in main.go" convention).
- [ ] **AC2.** When `WORKER_AUTH_TOKEN` is **unset or empty**: behavior is identical to today. POST `/_/worker/trigger` without an `Authorization` header → still 202 on valid payload, still 400 on invalid payload. No new failure mode introduced.
- [ ] **AC3.** When `WORKER_AUTH_TOKEN` is set: a request with `Authorization: Bearer <matching-token>` is accepted (202 / 400 paths unchanged).
- [ ] **AC4.** When `WORKER_AUTH_TOKEN` is set and the request has **no `Authorization` header**: respond `401 Unauthorized` + `WWW-Authenticate: Bearer realm="worker-trigger"` + body `"Unauthorized"`. The detached goroutine MUST NOT spawn.
- [ ] **AC5.** When `WORKER_AUTH_TOKEN` is set and the request has `Authorization` with a **wrong scheme** (e.g. `Basic foo`, or `Token foo`, or raw token without `Bearer`): respond 401 as above.
- [ ] **AC6.** When `WORKER_AUTH_TOKEN` is set and the request has `Authorization: Bearer <wrong-token>`: respond 401 as above.
- [ ] **AC7.** The token comparison uses `crypto/subtle.ConstantTimeCompare` so timing on per-byte comparison does not leak the expected token to brute-force attackers.
- [ ] **AC8.** The auth check runs **before** `json.NewDecoder(r.Body).Decode(...)` so unauthorized callers cannot cause JSON parse cost / log noise.
- [ ] **AC9.** GET read paths (the three URL regex families) are NOT affected. They remain unauthenticated; Cloudflare + LB controls remain the only auth there.
- [ ] **AC10.** Startup logs whether the worker-trigger auth is enabled or disabled (one line at startup).
- [ ] **AC11.** `make test` (Alpine) passes. `gofmt -l ./...` and `go vet ./internal/...` clean. `go.mod` / `go.sum` unchanged (uses only `crypto/subtle` from stdlib).
- [ ] **AC12.** README updated with `WORKER_AUTH_TOKEN` env var documentation under "Environment Variables" (deprecated section comes after).

## Non-Goals

- **JWT, signed timestamps, key rotation, per-tenant tokens.** Single shared secret only.
- **Rate limiting on 401 responses.** The endpoint is already low-traffic and the secret comparison is constant-time.
- **Auth on the GET read paths.** Out of scope; CDN-level auth is the model.
- **Hashing/peppering the token at rest.** The env var holds the raw token. Operator handles secret storage (k3s secret store).
- **Multiple acceptable tokens.** One token at a time; rotation = redeploy with a new env value.
- **Audit log of unauthorized attempts beyond the existing access log line.** The access log already records `status=401` per request; no separate audit stream.
