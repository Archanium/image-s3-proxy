---
project: "image-s3-proxy"
module: "root"
track_id: "add-timings-to-access-log"
generated_by: "draft:new-track"
generated_at: "2026-06-16T00:00:00Z"
git:
  branch: "add-timings-to-access-log"
  remote: "none"
  commit: "9ef919fc58db0e9452097b44533c62877e609c06"
  commit_short: "9ef919f"
  commit_date: "2026-06-14 22:47:12 +0200"
  commit_message: "feat: split origin/cache buckets + canary mode (off/shadow/live) (#2)"
  dirty: false
synced_to_commit: "9ef919fc58db0e9452097b44533c62877e609c06"
---

# Plan: Include phase timings in the JSON access log

| Field | Value |
|-------|-------|
| **Branch** | `add-timings-to-access-log` → none |
| **Commit** | `9ef919f` — feat: split origin/cache buckets + canary mode (off/shadow/live) (#2) |
| **Generated** | 2026-06-16T00:00:00Z |
| **Synced To** | `9ef919fc58db0e9452097b44533c62877e609c06` |

**Track ID:** add-timings-to-access-log
**Spec:** ./spec.md
**Status:** [x] Complete

## Overview

Single phase. Three layers of edits + one smoke + PR.

The `*Timings` accumulator already exists and already has `Phases() map[string]time.Duration`. The middleware already has `*Timings` in scope at emission time. This track is one struct field + one populate loop + tests.

## Phases

### Phase 1: Implement + verify + ship

**Goal:** Emit a top-level `timings` field on every JSON access log entry, populated from the per-request `*Timings`.
**Verification:** `make test` (Alpine) passes; live Docker smoke shows the new field on every emitted line.

#### Tasks

- [x] **Task 1.1:** `Entry.Timings map[string]float64` field added with `"timings"` tag, placed at the end of the struct. (88e2436)
- [x] **Task 1.2:** `Middleware` populates `entry.Timings` from `t.Phases()` via the existing `round3` helper. Always non-nil. (88e2436)
- [x] **Task 1.3:** `internal/accesslog/log_test.go` — extended `TestLogger_TopLevelKeyOrder` to assert `timings` is the last key; added `TestEntry_TimingsKeyAlwaysPresent` and `TestEntry_TimingsRoundTrip`. (88e2436)
- [x] **Task 1.4:** `internal/accesslog/middleware_test.go` — added `TestMiddleware_TimingsPopulatedFromContext`, `TestMiddleware_TimingsEmptyWhenNoPhases`, `TestMiddleware_TimingsSumEqualsUpstreamResponseTime`, `TestMiddleware_TimingsRespectsRound3`. (88e2436)
- [x] **Task 1.5:** `internal/server/server_test.go` — added `TestServeHTTP_AccessLog_OffMode_TimingsContents` and `TestServeHTTP_AccessLog_ShadowMode_TimingsContents` plus shared helpers `newMiddlewareCapturing` + `parseTimingsFromLog`. (99208aa)
- [x] **Task 1.6:** `gofmt -l ./...` clean; `go vet` clean.
- [x] **Task 1.7:** `make test` (Alpine) — all 5 packages pass; no regressions on existing 60+ tests.
- [x] **Task 1.8:** Docker smoke verified: `docker logs | jq '.timings'` shows `{"s3-get": 0.455}` on a request that ran one phase; `{}` on the worker-trigger path (no phases). `upstream.responseTime == sum(timings.values())` holds (0.455 == 0.455 in the smoke).
- [x] **Task 1.9:** README "Access log shape" section added; Features bullet updated. (48cbc9e)
- [ ] **Task 1.10:** Push + PR.

**Verification gate:**
- All existing tests pass under Alpine Docker.
- 4 new accesslog unit tests + 2 new server integration tests pass.
- Smoke: `docker logs ... | jq '.timings'` returns a non-null object on every captured line.
- `git diff go.mod go.sum` is empty.
- PR opened.

## Notes

- This track does NOT change the `Server-Timing` response header. That stays in milliseconds with 1-decimal precision; the JSON `timings` field is independent and uses seconds with 3-decimal precision. Different consumers, different conventions.
- Phase names are not introduced or validated here. They flow through `s.time(ctx, "...", ...)` exactly as before. If a future track adds e.g. `s3-get-cache` and `s3-get-origin` to disambiguate cache-hit vs candidate-key Gets, this track's emission machinery picks them up automatically.
- After deploy, the natural follow-ups (already enumerated as non-goals in `split-origin-and-cache-buckets` spec) become driven by real data: parallel candidate-key GETs (look at `s3-get` p95), fire-and-forget cache-back PUT (look at `s3-put-cache` p95 in live mode), R2 vs HOS comparison (compare `s3-get` p95 between shadow with X-Use-Cache: true vs default).
