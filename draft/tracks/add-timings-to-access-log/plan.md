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

- [ ] **Task 1.1:** Add `Timings map[string]float64` field to `Entry` in `internal/accesslog/log.go`. JSON tag `"timings"`. Placed at the end of the struct so the wire-format prefix is bit-stable for any downstream snapshot-test consumers.
- [ ] **Task 1.2:** Update `internal/accesslog/middleware.go`'s `Middleware` to populate `entry.Timings` from `t.Phases()`. Use the existing `round3` helper to convert each `time.Duration` to seconds with 3-decimal precision. Always assign a non-nil map (even when empty) so the JSON line always emits `"timings":{...}`.
- [ ] **Task 1.3:** Add tests in `internal/accesslog/log_test.go`:
  - `TestEntry_TimingsKeyAlwaysPresent` — emit an entry with zero phases; assert `"timings":{}` appears in the marshalled output and that the field's relative order is `[@timestamp, extra, user, request, response, upstream, timings]`.
  - `TestEntry_TimingsRoundTrip` — emit with phases `{"s3-get": 0.123, "resize": 0.045}`; parse back; assert exact values.
- [ ] **Task 1.4:** Add tests in `internal/accesslog/middleware_test.go`:
  - `TestMiddleware_TimingsPopulatedFromContext` — handler records `s3-get` 12 ms and `resize` 7 ms via `TimingsFromContext(r.Context()).Record(...)`; assert emitted line has `timings.s3-get == 0.012` and `timings.resize == 0.007`.
  - `TestMiddleware_TimingsEmptyWhenNoPhases` — no-op handler; assert `timings` is exactly `{}` (not absent, not null).
  - `TestMiddleware_TimingsSumEqualsUpstreamResponseTime` — record three phases summing to 100 ms; assert `upstream.responseTime` is within float epsilon of `sum(timings.values())`.
  - `TestMiddleware_TimingsRespectsRound3` — record 1.234567 ms (`1234567 ns`); assert emitted value is `0.001` (`round3(0.001234567)` = `0.001`).
- [ ] **Task 1.5:** Extend `internal/server/server_test.go`. Add two integration cases on top of existing fixtures:
  - `TestServeHTTP_AccessLog_OffMode_TimingsContents` — wrap server in `accesslog.Middleware` with a captured `bytes.Buffer` writer; resize a cache-miss URL; parse the emitted JSON; assert `timings` keys are exactly `{s3-get, resize, s3-put}` (off-mode phase names, bare `s3-put`).
  - `TestServeHTTP_AccessLog_ShadowMode_TimingsContents` — same but `NewServerWithMode(..., CacheModeShadow, ...)`; assert `timings` contains `{s3-get, resize, s3-put-cache, s3-put-origin}` and NOT bare `s3-put`.
- [ ] **Task 1.6:** Run `gofmt -l ./...` and `go vet ./internal/...`. Both clean.
- [ ] **Task 1.7:** Run `make test` (Alpine). All packages green.
- [ ] **Task 1.8:** Docker smoke. Rebuild image. Run with fake AWS creds in off mode. Hit one URL; verify the stdout JSON line contains a populated `timings` object via `docker logs | jq '.timings'`.
- [ ] **Task 1.9:** Update `README.md` — add a one-paragraph note to the access-log section explaining the new `timings` field: location (top-level), unit (seconds, 3 decimals), semantics (sparse, only phases that ran), relationship to `upstream.responseTime` (the sum) and `Server-Timing` header (same data, different format).
- [ ] **Task 1.10:** Commit each logical unit as its own commit. Push branch. Open PR titled `feat: include per-phase timings in JSON access log`. PR body lists the schema change, the additive guarantee, and references PR #1 (introduced phases) and PR #2 (added split-mode phase names).

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
