# Plan: Bearer-token auth on POST /_/worker/trigger

**Track ID:** add-worker-trigger-auth

## Phase 1: Complete

**Goal:** Add optional bearer-token auth on the worker trigger, gated by `WORKER_AUTH_TOKEN`. Backwards-compatible when unset.
**Verification:** `make test` (Alpine) passes; Docker smoke confirms 401 / 202 behavior in both modes.

### Tasks

- [ ] **Task 1:** Add `workerAuthToken string` field to `internal/server/server.Server` + `SetWorkerAuthToken(token string)` method (mirrors the existing `s3.Client.SetFallback` pattern — optional post-construction setup, no constructor signature churn).
- [ ] **Task 2:** Implement the check at the top of `handleWorkerTrigger`, BEFORE the JSON decode. Add a small helper `authorizeWorkerTrigger(r *http.Request) (ok bool)`:
  - If `s.workerAuthToken == ""`: return `true` (auth disabled).
  - Read `Authorization` header. Lowercase-compare the first 7 bytes against `"bearer "`; if no match → `false`.
  - Compare the remainder against `s.workerAuthToken` via `crypto/subtle.ConstantTimeCompare`; if 0 → `false`. If 1 → `true`.
  - On `false`: write `WWW-Authenticate: Bearer realm="worker-trigger"`, set status 401, body `"Unauthorized"`, return without spawning goroutine.
- [ ] **Task 3:** Tests in `internal/server/server_test.go` (`TestWorkerTrigger_Auth_*`):
  - Disabled (env empty) + missing header → 202 (back-compat).
  - Disabled + any garbage header → 202 (header is ignored).
  - Enabled + correct `Bearer <secret>` → 202.
  - Enabled + missing header → 401 + `WWW-Authenticate` header set + goroutine NOT spawned (use a channel-gated mock to assert).
  - Enabled + `Basic <secret>` (wrong scheme) → 401.
  - Enabled + `Bearer ` (empty token) → 401.
  - Enabled + `Bearer wrong-secret` → 401.
  - Enabled + correct token but lowercase `bearer` prefix → 202 (scheme is case-insensitive per RFC 6750).
- [ ] **Task 4:** Wire it in `cmd/image-proxy/main.go`: read `WORKER_AUTH_TOKEN` env var. If non-empty, call `srv.SetWorkerAuthToken(token)`. Log one startup line: `worker-trigger auth: ENABLED` or `worker-trigger auth: DISABLED (set WORKER_AUTH_TOKEN to enable)`.
- [ ] **Task 5:** Update `README.md` "Environment Variables" section: add `WORKER_AUTH_TOKEN` under a new "Auth" subsection (between "Server" and "Worker" sections, or at the end of the Server block) — note that an unset value disables the check and that the token must be sent as `Authorization: Bearer <token>`.
- [ ] **Task 6:** Run `gofmt -l ./...` + `go vet ./internal/...` + `make test` (Alpine). All clean.
- [ ] **Task 7:** Docker smoke. Three runs:
  - Run with no `WORKER_AUTH_TOKEN` env: POST a valid payload without `Authorization` → expect 202.
  - Run with `WORKER_AUTH_TOKEN=letmein`: POST without `Authorization` → expect 401 + `WWW-Authenticate` header. POST with `Authorization: Bearer wrong` → 401. POST with `Authorization: Bearer letmein` → 202.
  - Confirm via `docker logs` that the startup line reports the correct ENABLED/DISABLED state.
- [ ] **Task 8:** Push branch, open PR titled `feat: optional bearer-token auth on POST /_/worker/trigger`. PR body references architecture.md §9 gap #5 as the rationale.
