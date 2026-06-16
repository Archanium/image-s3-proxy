# Plan: Bearer-token auth on POST /_/worker/trigger

**Track ID:** add-worker-trigger-auth

## Phase 1: Complete

**Goal:** Add optional bearer-token auth on the worker trigger, gated by `WORKER_AUTH_TOKEN`. Backwards-compatible when unset.
**Verification:** `make test` (Alpine) passes; Docker smoke confirms 401 / 202 behavior in both modes.

### Tasks

- [x] **Task 1:** `workerAuthToken` field + `SetWorkerAuthToken` setter added to `Server`. (f09d2d3)
- [x] **Task 2:** `authorizeWorkerTrigger` + `unauthorized` helpers; check runs before JSON decode; constant-time comparison via `crypto/subtle`. (f09d2d3)
- [x] **Task 3:** 9 `TestWorkerTrigger_Auth_*` tests cover the back-compat default, correct token, case-insensitive scheme, missing header (with goroutine-must-not-spawn assertion via gated mock), wrong scheme, empty bearer, wrong token, and GET-read-paths-unaffected. (f09d2d3)
- [x] **Task 4:** `main.go` reads `WORKER_AUTH_TOKEN`; calls `SetWorkerAuthToken` when non-empty; startup log line reports ENABLED/DISABLED. (f09d2d3)
- [x] **Task 5:** README "Auth" subsection added between Server and Worker env-var blocks. (3e10333)
- [x] **Task 6:** `gofmt -l` clean; `make test` (Alpine) — 5/5 packages green.
- [x] **Task 7:** Docker smoke: disabled mode → startup log "DISABLED" + 202 on POST without header; enabled mode → startup log "ENABLED", no-header → 401 + `WWW-Authenticate: Bearer realm="worker-trigger"`, wrong token → 401, correct token → 202, GET read path → 404 (not 401).
- [x] **Task 8:** Push + PR — https://github.com/Archanium/image-s3-proxy/pull/5
