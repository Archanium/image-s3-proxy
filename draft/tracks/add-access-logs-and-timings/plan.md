---
project: "image-s3-proxy"
module: "root"
track_id: "add-access-logs-and-timings"
generated_by: "draft:new-track"
generated_at: "2026-06-12T12:55:00Z"
git:
  branch: "add-access-logs-and-timings"
  remote: "none"
  commit: "21fdfb3c6f1fc77bc3f10c54bd56e804e5ff0d86"
  commit_short: "21fdfb3"
  commit_date: "2026-05-07 18:46:19 +0200"
  commit_message: "fix: updating the tests and fixing an error on the normalized key"
  dirty: false
synced_to_commit: "21fdfb3c6f1fc77bc3f10c54bd56e804e5ff0d86"
---

# Plan: Structured access logs + Server-Timing header

| Field | Value |
|-------|-------|
| **Branch** | `add-access-logs-and-timings` → none |
| **Commit** | `21fdfb3` — fix: updating the tests and fixing an error on the normalized key |
| **Generated** | 2026-06-12T12:55:00Z |
| **Synced To** | `21fdfb3c6f1fc77bc3f10c54bd56e804e5ff0d86` |

**Track ID:** add-access-logs-and-timings
**Spec:** ./spec.md
**Status:** [x] Complete

## Overview

Three phases. Each phase is a self-contained, commit-able milestone.

- **Phase 1 — Foundation:** new `internal/accesslog` package, types, and tests. No server changes. The package can be imported and unit-tested in isolation.
- **Phase 2 — Wire it up:** add the middleware in `main.go`, thread `*Timings` through `context`, instrument every S3 + resize call site in `server.go`, reorder `handleResize` so `s3.Put` precedes `w.Write`, add the few integration assertions to `server_test.go`.
- **Phase 3 — Verify + ship:** run all tests, microbenchmark the middleware overhead, smoke-test with `curl`, confirm Q1 (cookie PII), submit for review.

Phases 1 and 2 should be separate commits to keep review tractable. Phase 3 produces no production-code changes, only verification artifacts.

## Phases

### Phase 1: Foundation — `internal/accesslog` package

**Goal:** Build the access-log + timings package in isolation with full unit-test coverage. Nothing in `server` or `main` changes yet.
**Verification:** `go test -v ./internal/accesslog/...` passes; package compiles standalone.

#### Tasks

- [x] **Task 1.1:** Create directory `internal/accesslog/`. (b0d514d)
- [x] **Task 1.2:** Implement `internal/accesslog/timings.go`. (b0d514d)
- [x] **Task 1.3:** Implement `internal/accesslog/context.go`. (da4df2c)
- [x] **Task 1.4:** Implement `internal/accesslog/writer.go`. (9df7f26)
- [x] **Task 1.5:** Implement `internal/accesslog/log.go` — `Logger`, `Entry` types. (e2c66a5)
- [x] **Task 1.6:** Implement `internal/accesslog/middleware.go`. (68b44bd)
- [x] **Task 1.7:** Implement `internal/accesslog/middleware_test.go` — 14 tests including correlationId precedence (3 cases), response X-Request-ID echo, schema with no `cart`, scheme detection, status/bytes capture, upstream sum, Server-Timing header, X-Forwarded-For parsing, CF-Connecting-IP field, exactly-one-line-per-request, and a no-op-handler benchmark. (68b44bd)
- [x] **Task 1.8:** Implement `internal/accesslog/timings_test.go` — 10 tests including Record accumulation, Track error propagation, header order (known then alphabetical extras), empty header, nil-receiver safety, snapshot isolation, and concurrent-Record race-freedom. (b0d514d)
- [x] **Task 1.9:** Implement `internal/accesslog/writer_test.go` — 9 tests including default status 200, implicit/explicit WriteHeader, byte accumulation, Server-Timing-on-first-WriteHeader, post-write phases excluded from header but kept in Timings, duplicate WriteHeader ignored, nil-Timings safe. (9df7f26)
- [x] **Task 1.10:** Run `go test -race -v ./internal/accesslog/...` — all 41 tests pass; `go vet` and `go build` clean.

**Verification gate:**
- All Phase-1 tests pass with `-race`.
- The package compiles in isolation: `go build ./internal/accesslog/...`.
- No imports outside the stdlib.

### Phase 2: Wire it up

**Goal:** Wire the middleware into `main.go`, instrument the four phase call-sites in `server.go`, reorder `handleResize`'s Put-then-Write, and confirm existing `server_test.go` cases still pass.
**Verification:** `make test` (Alpine) passes locally and in CI.

#### Tasks

- [x] **Task 2.1:** Wire `accesslog.Middleware` in `cmd/image-proxy/main.go`; upstreamHost prefers `S3_ENDPOINT` over the bucket name. (d0e85e0)
- [x] **Task 2.2:** Add `(s *Server).time(ctx, phase, fn)` helper in `internal/server/server.go`. (d0e85e0)
- [x] **Task 2.3:** Instrument `ServeHTTP` — 2× `s3-exists` (initial + normalized-key) and 2× `s3-get` (cached-hit + normalized-key) call sites. (d0e85e0)
- [x] **Task 2.4:** Instrument `handleResize` — candidate-key `s3-get` loop, `resize`, and `s3-put`. Confirmed Put-before-Write order already present in original code; no reorder needed. (d0e85e0)
- [x] **Task 2.5:** Instrument `handleFile` — `s3-get` + `s3-put`. Confirmed Put-before-Write order already present. (d0e85e0)
- [x] **Task 2.6:** `handleWorkerTrigger` left untouched; detached goroutine uses `context.Background()` and the response carries no S3 phases. (d0e85e0)
- [x] **Task 2.7:** Added 4 integration tests to `server_test.go`: cached-hit Server-Timing, resize 4-phase Server-Timing, 404-still-emits-s3-phases, X-Request-ID echo. (d0e85e0)
- [x] **Task 2.8:** `make test` (Alpine) and `make test-debian` both pass; no regressions on existing 25+ tests; all 4 new integration tests pass.
- [x] **Task 2.9:** `gofmt -l` reports clean on all touched files (no rewrites needed).

**Verification gate:**
- `make test` and `make test-debian` both pass.
- `go vet ./...` passes.
- No new entries in `go.mod` / `go.sum`.

### Phase 3: Verify + ship

**Goal:** End-to-end verification, microbenchmark, resolve the one blocking Open Question, prep for merge.
**Verification:** Manual smoke test with `curl`; microbenchmark report; Q1 resolved.

#### Tasks

- [x] **Task 3.1:** ~~Resolve Open Question Q1: confirm with the owner whether the `cart` cookie value can be logged verbatim.~~ **Resolved 2026-06-12: drop the field entirely — image service has no use for it.** Spec and Phase-1 tests updated accordingly.
- [x] **Task 3.2:** Microbenchmark: `BenchmarkMiddleware_NoopHandler-10  2400 ns/op  2797 B/op  24 allocs/op` on Apple M1 Max. At 2.4 µs/request the overhead is < 0.025% even on the cheapest cached-hit path (S3 GET dominates at ~10ms+). Well under the 5% target. No `sync.Pool` work needed.
- [x] **Task 3.3:** Live smoke test run via `docker run` against `archanium/image-s3-proxy:alpine-latest` (built from this branch). Three requests:
   1. `POST /_/worker/trigger` with `X-Request-ID: smoke-worker-001` → `202 Accepted`, response `X-Request-Id: smoke-worker-001`, log line shows `status=202, bytes=8, upstream.responseTime=0` (no S3 phases for fire-and-forget worker). ✓ AC8.
   2. `GET /13/2/images/products/240/336/foo.jpg` with `X-Request-ID: smoke-resize-002` → `404` (fake AWS creds so S3 returns 403), response `X-Request-Id: smoke-resize-002`, response `Cache-Control: max-age=30`, response `Server-Timing: s3-exists;dur=460.3, s3-get;dur=1715.5`, log line shows `status=404, bytes=19, upstream.responseTime=2.176`. ✓ AC2, AC4, AC9.
   3. `GET /some/missing-key.jpg` (no X-Request-ID, no CF-Ray) → `404`, response `X-Request-Id: 156dcd3a7089b9c00fb4c9173e93b7fa` (32-char lowercase hex), Server-Timing has `s3-exists` only. ✓ AC2.
   - All three log lines parsed cleanly with `jq -c`. Confirmed via `jq` that every line has top-level keys `[@timestamp, extra, request, response, upstream, user]` and user keys `[agent, cloudflare, ip, name, referrer]` — no `cart` anywhere. ✓ AC1, AC6.
   - `upstream.upstreamHost = "smoke-test"` (the bucket name was used because no S3_ENDPOINT was set). ✓ AC5.
- [x] **Task 3.4:** Manual deploy-checklist:
   - **Cache-Control:** `max-age=31536000` on 2xx, `max-age=30` on errors — preserved (verified by smoke + existing TestErrorCaching tests).
   - **No new env vars** — confirmed (main.go only reads existing env vars).
   - **No go.mod changes** — confirmed (`git diff go.mod go.sum` empty).
   - **libvips lifecycle** — unchanged (no edits to resizer.go or vips Startup/Shutdown lines).
   - **Worker fire-and-forget contract (I10)** — preserved (smoke test 1 confirmed).
   - **Cross-platform** — `make test` (Alpine) and `make test-debian` both pass.
- [ ] **Task 3.5:** Commit draft/ context, push branch, open PR. **Awaiting user confirmation before pushing (visible-to-others action).**

**Verification gate:**
- All three smoke-test `curl`s produced well-formed JSON + Server-Timing as described. ✓
- Microbenchmark overhead 2.4 µs/req — < 0.025% on the cheapest path; well under the 5% target. ✓
- PR creation pending user authorization to push.

## Notes

- The plan deliberately keeps Phase 1 isolated so the package can land as its own commit and be reviewed without server-side context.
- The most important review point in Phase 2 is the order of `s3.Put` and `w.Write` in `handleResize` and `handleFile`. The current code already has Put before Write, so no reordering is needed — but the plan task explicitly verifies this so the reviewer doesn't have to.
- If Q1 forces us to drop or hash `user.cart`, the change is contained to the middleware's `Entry` construction — no schema break for monitoring queries.
- After this track ships, the natural follow-up tracks are: (a) per-tenant `clientId` extraction from the URL into a top-level log field for tenant-level dashboards; (b) reading the log stream into a Prometheus exporter; (c) propagating `traceparent` for true distributed tracing.
