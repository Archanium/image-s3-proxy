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

# Specification: Include phase timings in the JSON access log

| Field | Value |
|-------|-------|
| **Branch** | `add-timings-to-access-log` → none |
| **Commit** | `9ef919f` — feat: split origin/cache buckets + canary mode (off/shadow/live) (#2) |
| **Generated** | 2026-06-16T00:00:00Z |
| **Synced To** | `9ef919fc58db0e9452097b44533c62877e609c06` |

**Track ID:** add-timings-to-access-log
**Status:** [x] Complete

## Context References

- **Product:** `draft/product.md` — advances the "observability beyond log.Printf" goal directly. Today `Server-Timing` carries per-phase durations on the response header but the JSON access log only carries the **sum** under `upstream.responseTime`. Monitoring backends that ingest the log stream (without parsing response headers) have no per-phase visibility — exactly the metric needed to drive the next batch of follow-up tracks (parallel candidate-key GETs, fire-and-forget cache-back PUT, R2 vs HOS p95 comparison).
- **Tech Stack:** `draft/tech-stack.md` — uses the existing `internal/accesslog.Timings` accumulator and the existing `encoding/json` emission path. Zero new dependencies.
- **Architecture:** `draft/.ai-context.md` § ACCESS_LOG / Server-Timing — the `*Timings` instance is already on the request context, the snapshot is already exposed via `Timings.Phases() map[string]time.Duration`. The middleware just needs to read it and populate a new typed field on `Entry` before emission.
- **Prior tracks:** `add-access-logs-and-timings` (PR #1) introduced the Server-Timing header + 4 phase names. `split-origin-and-cache-buckets` (PR #2) added two more (`s3-put-cache`, `s3-put-origin`) and removed `s3-exists`. This track makes those phases visible in the log stream too.

## Problem Statement

`Server-Timing` exposes per-phase durations on every 2xx/3xx response, but most monitoring infrastructure ingests the JSON access log line (parsed from `stdout`) rather than the response headers. As a result, the only timing visible in the dashboard today is `upstream.responseTime` — the **sum** of all phases — which collapses three distinct optimization signals (S3 latency, libvips CPU, cache-back PUT cost) into one number.

This blocks data-driven decisions on the planned follow-up tracks (parallel candidate-key GETs, fire-and-forget cache-back PUT, R2 vs HOS p95 comparison): all three depend on knowing **which phase** dominates p99, and that information is in the proxy but not in the log stream.

## Background & Why Now

- The `*Timings` accumulator already records per-phase durations. It already exposes a `Phases() map[string]time.Duration` snapshot. The middleware already has it in scope at emission time.
- Adding a sparse `timings` field to the JSON log is a one-line typed-field addition + a one-line populate. Zero risk to the existing schema; zero new dependencies.
- The follow-up tracks listed in the prior PR descriptions explicitly want this data.

## Requirements

### Functional

1. **New top-level `timings` field on the JSON access log entry.** Sibling to `upstream`, not nested inside it.
   - JSON shape: `"timings": { "<phase>": <seconds>, ... }`
   - Phase names match `Server-Timing` exactly: `s3-get`, `resize`, `s3-put`, `s3-put-cache`, `s3-put-origin`, and any future phase name passed through `s.time(ctx, "...", ...)`.
   - Values are floats representing wall-clock duration in **seconds with 3-decimal precision**. Same unit as `request.time` and `upstream.responseTime` (the log already uses seconds; the Server-Timing header continues to use milliseconds independently).
   - Sparse: only phases that actually ran on this request appear. A cache-hit request emits `{"s3-get": 0.012}`; a worker-trigger request emits `{}` (no phases ran).
   - Always present (not `omitempty`): even when no phases ran, the entry contains `"timings": {}` so log-parsing queries that test for field presence don't break.

2. **`upstream.responseTime` invariant preserved.** Continues to be the sum of all recorded phase durations in seconds. With the new `timings` field present, this is now derivable client-side; both fields are emitted for backwards compatibility.

3. **Existing fields unchanged.** Field order, names, types, and semantics of `@timestamp`, `extra`, `user`, `request`, `response`, `upstream` are bit-for-bit identical. The `timings` field is appended after `upstream` so existing log-parser snapshots and field-position-dependent tooling don't break.

### Non-Functional

1. **Zero new dependencies.** Implementation uses only `encoding/json` and `time` (already imported).
2. **Negligible overhead.** Per-request cost is one map iteration + one `math.Round`-per-phase; well under 1 µs.
3. **Thread safety.** `Timings.Phases()` already returns a snapshot copy (verified in unit tests from the prior track); the middleware reads it after the inner handler returns, so no race.
4. **Backwards compatibility.** Log schema is purely additive. No existing monitoring queries break.

## Acceptance Criteria

- [ ] **AC1 (field present).** Every JSON access log line includes a top-level `"timings"` key. The key is always present, even when its value is an empty object.
- [ ] **AC2 (phase names match Server-Timing).** Phase keys inside `timings` are exactly the strings recorded via `s.time(ctx, "...", ...)`, identical to the names that appear in the `Server-Timing` response header.
- [ ] **AC3 (units in seconds, 3 decimals).** Each value is a JSON number with at most 3 decimal places of precision, expressed in seconds. (Example: a 14.7 ms phase appears as `0.015`.)
- [ ] **AC4 (sparse).** Phases that did NOT run on a request are absent from `timings`. A cache-hit request emits exactly one entry (`s3-get`); a worker-trigger request emits `{}`.
- [ ] **AC5 (sum invariant).** `upstream.responseTime` equals the sum of values in `timings` (within float rounding tolerance — both are derived from the same `*Timings` accumulator).
- [ ] **AC6 (off-mode `s3-put`).** A cache-miss resize request in `CACHE_MODE=off` produces `timings: {"s3-get": ..., "resize": ..., "s3-put": ...}` (bare `s3-put`, matching today's off-mode Server-Timing).
- [ ] **AC7 (shadow/live split phases).** A cache-miss resize request in `CACHE_MODE=shadow` or `live` produces `timings` containing both `s3-put-cache` and `s3-put-origin` (and no bare `s3-put`).
- [ ] **AC8 (existing-line shape unchanged).** Every JSON key from the prior log shape (`@timestamp`, `extra.*`, `user.*`, `request.*`, `response.*`, `upstream.*`) is still present with the same name, type, and order.
- [ ] **AC9 (tests pass).** `make test` (Alpine) passes; new unit tests in `internal/accesslog` for `Entry.Timings` and middleware population; new assertion in `internal/server` integration tests; existing tests unchanged.
- [ ] **AC10 (no new deps).** `go.mod` and `go.sum` unchanged.

## Non-Goals

- **Renaming or repurposing existing fields.** `upstream.responseTime` stays as the sum. `timings` is additive.
- **Changing the `Server-Timing` response header.** Its format stays the same (milliseconds, 1 decimal).
- **Sampling or filtering.** Every request logs its timings.
- **Emitting durations in nanoseconds, microseconds, or milliseconds.** Seconds with 3-decimal precision is the only unit. Different units for header vs log is acceptable because the consumers are different (browsers parse the header, log shippers parse the JSON).
- **Adding a flag to disable the new field.** The field is always emitted; it's cheap and additive.
- **Changing how phase names are chosen.** They are whatever `s.time(ctx, "...", ...)` callers pass. This track does not introduce a phase-name registry or validation.

## Technical Approach

### Entry struct addition

```go
// internal/accesslog/log.go
type Entry struct {
    Timestamp string             `json:"@timestamp"`
    Extra     EntryExtra         `json:"extra"`
    User      EntryUser          `json:"user"`
    Request   EntryRequest       `json:"request"`
    Response  EntryResponse      `json:"response"`
    Upstream  EntryUpstream      `json:"upstream"`
    Timings   map[string]float64 `json:"timings"`
}
```

A `map[string]float64` (not a typed struct) because phase names are not a closed set — callers add new ones via `s.time(...)` without round-tripping through this package.

Field order matters for log-parser stability: `Timings` is appended at the end, so prior-shape snapshots remain prefix-equal.

### Middleware population

In `Middleware` (`internal/accesslog/middleware.go`):

```go
phaseSnapshot := t.Phases() // already returns a copy
timings := make(map[string]float64, len(phaseSnapshot))
for name, d := range phaseSnapshot {
    timings[name] = round3(d.Seconds())
}
entry.Timings = timings
```

Note: `round3` already exists in `middleware.go` and is used for `request.time` / `upstream.responseTime`. Reuse it.

When `len(phaseSnapshot) == 0` (worker trigger), `timings` is an empty non-nil map. JSON marshalling emits `"timings":{}` — always-present key per AC1.

### Why `map[string]float64` over a typed struct

- Phase names are dynamic. `s.time(ctx, "...", ...)` is the registration point; future tracks may add new names (already happened: this track is the third PR to add phase names). A typed struct would force an update here on every new phase.
- JSON marshalling of a `map[string]float64` emits keys in alphabetical order (Go stdlib behavior). Phase ordering inside `timings` is therefore deterministic and stable across runs, which matters for log-diffing.
- Tests can assert membership and value tolerance directly on the map without struct-field reflection.

### Field placement in the struct

`Timings` goes at the **end** of `Entry` (after `Upstream`) so the wire-format prefix is bit-identical for log parsers that key on byte offsets or that have snapshot-tested prior outputs. This is a minor concern but cheap to honor.

### Files touched

- `internal/accesslog/log.go` — `Entry.Timings` field.
- `internal/accesslog/middleware.go` — populate `Timings` from the per-request `*Timings`.
- `internal/accesslog/log_test.go` — new test asserting `timings` key always present + correct shape.
- `internal/accesslog/middleware_test.go` — new test asserting populated values, sparse semantics, and sum invariant.
- `internal/server/server_test.go` — extend an existing integration test to assert `timings` content reflects the request's phase set (off-mode resize → s3-get/resize/s3-put; shadow-mode resize → s3-get/resize/s3-put-cache/s3-put-origin).
- README.md — add a one-paragraph note under the access-log section explaining the new field.

### What is NOT touched

- `internal/accesslog/timings.go` — `Timings.Phases()` already provides the snapshot in the right shape.
- `internal/accesslog/writer.go` — Server-Timing emission is independent of the log entry.
- `internal/server/server.go` — no server changes; this track is wholly inside `accesslog`.

## Success Metrics

| Category | Metric | Target | Measurement |
|----------|--------|--------|-------------|
| Quality | All existing tests pass | 100% | `make test` (Alpine) |
| Quality | New unit + integration tests for `timings` | ≥ 4 cases | hand-review of new tests |
| Observability | After deploy, monitoring backend can build a "per-phase p99" panel | Yes | confirm one panel after deploy |
| Compat | Existing log schema bit-stable on the documented keys | 100% | unit test asserts existing top-level keys are unchanged and same order |

## Stakeholders & Approvals

| Role | Name | Approval Required | Status |
|------|------|-------------------|--------|
| Owner | thomas@kasasagi.dk | Spec sign-off, deploy sign-off | [x] (single-maintainer project) |

### Approval Gates

- [x] Spec approved (single maintainer)
- [x] Architecture reviewed — additive, scoped to one package
- [x] Security review N/A — no new data sources; the values emitted are wall-clock durations already in the Server-Timing header

## Risk Assessment

| Risk | Probability | Impact | Score | Mitigation |
|------|-------------|--------|-------|------------|
| Field order change breaks a downstream log parser that snapshot-tests JSON keys | 2 | 2 | 4 | `Timings` is appended at the end of `Entry`; the existing key sequence is bit-stable. Unit test asserts. |
| `map[string]float64` serialization order is non-deterministic in some configurations | 1 | 1 | 1 | Go stdlib's `encoding/json` sorts map keys alphabetically (documented behavior); confirmed in standard tests. |
| Rounding introduces a discrepancy between `upstream.responseTime` and `sum(timings)` | 2 | 1 | 2 | Both fields go through the same `round3` helper. Test asserts the two are equal within a small float epsilon. Documented as "within float rounding tolerance" in AC5. |
| Field is always emitted even when empty, adding ~12 bytes to every log line | 5 | 1 | 5 | Acceptable — log line is already ~700+ bytes; the cost is < 2%. The benefit (always-present field for monitoring queries) outweighs it. |
| Phase name "s3-get" appearing on a cache-hit request blurs the distinction from a candidate-key Get on a miss (both are recorded under the same phase) | 3 | 1 | 3 | This is true today in `Server-Timing` and not a regression. If granularity becomes a problem, a follow-up track can introduce `s3-get-cache` / `s3-get-original` phase names at the call sites. |

## Deployment Strategy

Additive log schema change. Deploy at any time. No env-var toggle, no canary needed.

### Rollback Plan

- **Trigger:** monitoring shows malformed log lines or a downstream parser explicitly rejects the new field.
- **Process:** revert the merge commit, redeploy.
- **Data rollback:** N/A.

## Open Questions

| # | Question | Owner | Resolution |
|---|----------|-------|------------|
| Q1 | Locked-in design: top-level `timings` (not nested under `upstream`), seconds with 3 decimals, always-present (no `omitempty`), name match with Server-Timing. Override if any of these doesn't fit. | owner | proposed; pending implicit acceptance |

## Conversation Log

- **Decision:** Top-level `timings` field, not nested under `upstream`. Rationale: `upstream` is repurposed for nginx schema parity; adding `phases` under it would suggest nginx has the concept, which it doesn't.
- **Decision:** Values in seconds with 3-decimal precision. Matches `request.time` and `upstream.responseTime` inside the log. The `Server-Timing` response header continues to use milliseconds independently — different consumers, different conventions.
- **Decision:** `map[string]float64`, not a typed struct. Phase names are dynamic (this track is the third PR to add new phase names); a typed struct would force a constant churn here.
- **Decision:** Always-present field, no `omitempty`. Monitoring queries that test for key presence don't break.
- **Decision:** `Timings` is appended at the end of `Entry`, so the existing wire-format prefix is bit-stable for log-parser snapshots.
