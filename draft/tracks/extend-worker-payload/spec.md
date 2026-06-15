---
project: "image-s3-proxy"
module: "root"
track_id: "extend-worker-payload"
generated_by: "draft:new-track"
generated_at: "2026-06-16T01:00:00Z"
git:
  branch: "extend-worker-payload"
  remote: "none"
  commit: "9ef919fc58db0e9452097b44533c62877e609c06"
  commit_short: "9ef919f"
  commit_date: "2026-06-14 22:47:12 +0200"
  commit_message: "feat: split origin/cache buckets + canary mode (off/shadow/live) (#2)"
  dirty: false
synced_to_commit: "9ef919fc58db0e9452097b44533c62877e609c06"
---

# Specification: Multi-image / multi-format worker trigger envelope

| Field | Value |
|-------|-------|
| **Branch** | `extend-worker-payload` → none |
| **Commit** | `9ef919f` — feat: split origin/cache buckets + canary mode (off/shadow/live) (#2) |
| **Generated** | 2026-06-16T01:00:00Z |
| **Synced To** | `9ef919fc58db0e9452097b44533c62877e609c06` |

**Track ID:** extend-worker-payload
**Status:** [x] Complete

## Context References

- **Product:** `draft/product.md` — directly addresses P1 issue from architecture.md §9 #2: "Hard-coded clientId in worker.go:78". The catalog system needs to pre-warm thumbnails for *any* client, not just `clientId=13`.
- **Tech Stack:** `draft/tech-stack.md` — stays inside the stdlib + AWS SDK v2 idiom. `encoding/json` for parsing, no schema-validation library, no DI changes.
- **Architecture:** `draft/.ai-context.md` § FLOW:worker_trigger — re-shapes the trigger payload from `{"key":"..."}` to a richer envelope. Closes the hardcoded `clientId=13` dead-end documented at `architecture.md §9 #2`. Worker fire-and-forget contract (I10) preserved — the trigger still returns 202 immediately.
- **Prior tracks:** `split-origin-and-cache-buckets` (PR #2) made the worker dual-write to origin AND cache when both are configured; this track reuses that dual-write path verbatim — no changes to the put-side. `add-timings-to-access-log` (PR #3) emits per-phase durations in the JSON log; the new batched worker calls don't run on the request path, so they don't surface in those timings (the trigger itself is 202 with no S3 phases, same as today).

## Problem Statement

The worker trigger today accepts only `{"key": "catalog/products/images/foo.jpg"}` — a single image, processed against the env-configured `SIZES` and `FORMAT`, with the output S3 key path hardcoded to `13/{version}/images/products/...`. Two concrete consequences:

1. **The worker is only correct for `clientId=13`.** Any other tenant invoking the trigger gets thumbnails written under the wrong S3 prefix — silently, because the proxy never reads back from the worker's destination. This is `architecture.md §9 #2`, dormant in the codebase since launch.
2. **No way to pre-warm multiple images, sizes, or formats in one request.** The caller must fire one POST per image, and the env-wide `SIZES` / `FORMAT` apply to every call indiscriminately. For a catalog system that knows exactly which (image × size × format) tuples it needs, this is N round-trips instead of one.

The catalog system upstream of this proxy can produce per-batch metadata (clientId, image URIs, target sizes, target formats, version). A richer envelope passes that information through.

## Background & Why Now

- `architecture.md §9 #2` flagged the hardcoded clientId as a known limitation in the v1 init. The split-bucket track left a note: "if you touch worker code, treat that constant as a TODO and thread the clientId from the trigger payload or the matched key."
- The catalog system upstream is being updated to send multi-format batches (AVIF + WebP per size) for newly uploaded product imagery. Doing this with the current single-image, single-format trigger would require ~6× more POSTs per upload.
- The worker fire-and-forget contract (I10) means we can keep the trigger's 202-Accepted latency identical no matter how much work the batch represents. Goroutine lifetime grows; HTTP response latency does not.

## Requirements

### Functional

1. **New trigger payload shape** (JSON):
   ```json
   {
     "clientId": "39",
     "version": "3",
     "images": ["{productUri}/{imageSrc}", "..."],
     "sizes": [[200,0],[400,0]],
     "formats": ["avif","webp"]
   }
   ```
   - `clientId` — **required**, non-empty string. Becomes the leading prefix of every output S3 key (replaces today's hardcoded `13/`).
   - `images` — **required**, non-empty array of strings. Each entry is a fully-resolved S3 key to the original image (under the origin bucket). The proxy does no template substitution — the upstream catalog system resolves `{productUri}/{imageSrc}` server-side before posting.
   - `formats` — **required**, non-empty array of strings. Each must be one of `png`, `jpg`, `jpeg`, `webp`, `avif` (the set the resizer supports). Invalid entries → 400 with the offending value named.
   - `sizes` — **optional**. When present: non-empty array of two-element integer arrays `[width, height]`; each value must be ≥ 0. When absent or `null`: the worker's env-configured sizes are used (`w.sizes`, populated from `SIZES` env at startup, defaulting to `worker.DefaultSizes`).
   - `version` — **optional** string. When present: must parse via `strconv.Atoi` (any base-10 integer ≥ 0). When absent / empty string: defaults to `3` (matches today's hardcoded value).

2. **Legacy `{"key": "..."}` payload is rejected with HTTP 400.** No grace period. The catalog system migrates first; the new payload is deployed second.

3. **Output S3 key shape** — replaces the current hardcoded prefix:
   ```
   {clientId}/{version}/images/products/{width}/{height}/{origFilename}.{format}
   ```
   - `origFilename` = `filepath.Base(image)` (unchanged from today).
   - `folder` segment stays `products` — out of scope for this track (see Non-Goals).

4. **Cartesian fan-out per request.** For each `image` in `images`, for each `(width, height)` in the effective sizes, for each `format` in `formats`: produce one resized thumbnail. Total outputs per request = `|images| × |sizes| × |formats|`.

5. **Existing per-output semantics preserved:**
   - Skip-existing check (against the cache client when `CACHE_MODE` is shadow/live; against the single client in off mode) — unless `w.forceOverwrite` is true. `forceOverwrite` continues to be hardcoded `false` at server-side construction.
   - Dual-write to both origin and cache when distinct (matches the split-bucket pattern from PR #2). Per-side failures are logged independently and do not abort the batch loop.
   - Single resize per `(image, width, height, format)` tuple — the resizer is called once per output.

6. **Sequential processing within the detached goroutine.** Same goroutine model as today (`go func() { ... }` from `handleWorkerTrigger`). Images are processed in order; sizes inside an image in order; formats inside a size in order. The HTTP response is still 202 returned before the goroutine starts.

7. **Validation runs synchronously, before the 202.** A malformed payload returns 400 with a body describing the first validation failure encountered. Successful validation → 202 + detached goroutine.

8. **`worker.ProcessProductImage` and `worker.ProcessS3Event` are removed.** They were both only callable via the now-removed legacy payload path. Their dead code goes with the migration.

9. **`worker.NewWorker` signature unchanged.** The constructor still takes the env-wide `sizes` and `format`. `sizes` continues to populate `w.sizes` as the fallback when a request omits `sizes`. `format` is no longer the default for batch processing (the new payload requires `formats` explicitly) — but the env value is kept on the struct in case future internal callers need it.

10. **Worker fire-and-forget contract (architecture.md §2 I10) preserved.** `POST /_/worker/trigger` still returns 202 immediately. The detached goroutine still uses `context.Background()` so the HTTP request's cancellation does not propagate.

### Non-Functional

1. **Zero new go.mod entries.** Implementation uses only `encoding/json`, `strconv`, `path/filepath`, and the existing internal imports.
2. **No semantic change to non-trigger code paths.** `ServeHTTP` (the read path) is untouched. The `handleResize` / `handleFile` cache-back PUTs are untouched. The Server-Timing instrumentation is untouched.
3. **No new env vars.** Configuration surface is unchanged.

## Acceptance Criteria

- [ ] **AC1 (happy path).** A POST with the example payload returns 202 immediately. The detached goroutine produces `|images| × |sizes| × |formats|` thumbnails under the documented key shape.
- [ ] **AC2 (clientId no longer hardcoded).** With `clientId: "39"`, the output S3 key prefix is `39/...`, not `13/...`. The string `13/` does not appear anywhere in `internal/worker` after the refactor.
- [ ] **AC3 (multi-format per size).** With `formats: ["avif", "webp"]`, each size produces two outputs — one `.avif` and one `.webp` — with their respective `Content-Type`s set by the resizer.
- [ ] **AC4 (sizes default to env).** With `sizes` absent or `null`, the worker uses its `w.sizes` field (which is populated from the `SIZES` env at startup, or `DefaultSizes` if `SIZES` was unset).
- [ ] **AC5 (formats required).** A payload without `formats` (or with `formats: []` or `formats: null`) returns 400 with a body naming the missing/empty field.
- [ ] **AC6 (clientId required).** A payload with `clientId: ""` or absent returns 400.
- [ ] **AC7 (images required).** A payload with `images: []` or absent returns 400.
- [ ] **AC8 (version default).** With `version` absent or empty, the output key contains `/3/` (defaults to 3).
- [ ] **AC9 (version invalid).** With `version: "abc"`, returns 400 naming the field. With `version: "2"`, the output key contains `/2/`.
- [ ] **AC10 (invalid format value).** A payload with `formats: ["avif", "tiff"]` returns 400 naming `tiff` as the invalid value (validates against the allowed set `{png, jpg, jpeg, webp, avif}`).
- [ ] **AC11 (legacy payload rejected).** `{"key": "catalog/products/images/foo.jpg"}` returns 400 (no `images` array → fails the new schema). The error body explicitly mentions the new field names so callers can self-correct.
- [ ] **AC12 (202 latency contract preserved).** The HTTP response is 202 Accepted with body "Accepted" within milliseconds of receipt, regardless of payload batch size. The detached goroutine runs after the response is sent (test mock verifies `Worker.ProcessBatch` is invoked asynchronously and that the handler returns before it starts).
- [ ] **AC13 (skip-existing check honors split mode).** When `CACHE_MODE=shadow` or `live`, the skip-if-exists check targets the cache bucket (existing behavior from PR #2 — unchanged by this track). When off, it targets the single client.
- [ ] **AC14 (dual-write preserved in split mode).** When the worker's `s3Client` and `destS3Client` are distinct, each successful resize Puts to both, with per-side failures logged but not aborting the batch.
- [ ] **AC15 (no go.mod / go.sum change).** `git diff go.mod go.sum` empty.
- [ ] **AC16 (tests + format).** `make test` (Alpine) passes. `gofmt -l` and `go vet ./...` clean.

## Non-Goals

- **Adding `folder` to the payload.** Stays hardcoded to `products`. If pre-warming branding / blocks via the worker becomes needed, separate track.
- **Parallel processing within the goroutine.** Sequential keeps libvips memory predictable and the code straightforward. If batch latency becomes operationally painful (note: it's fire-and-forget, so this would mean "the resized thumbs aren't ready when users hit them"), separate track.
- **A graceful migration window for the legacy payload.** Old shape returns 400 immediately. The catalog system upstream coordinates the deploy ordering.
- **Per-image error reporting back to the caller.** The HTTP response is 202 before the work starts, so per-image failures are observable only via logs. Same as today.
- **Image-by-image idempotency / batch dedup.** The detached goroutine may be invoked multiple times with the same batch (e.g. retried by the catalog system). The skip-existing check (per output) handles idempotency at the S3 level; explicit dedup is not added.
- **Validation of S3-key shape (e.g. that `images[i]` resembles a real key).** Garbage-in is permitted; the worker will Get-404 and log per image without aborting the batch.
- **Schema versioning of the envelope.** First and only version; no `v` field. If the shape ever changes, a separate URL path is the migration tool, not a versioned envelope.
- **Re-introducing `ProcessS3Event` for any caller.** Both the method and the URL it was reached from (the legacy trigger) go away together.

## Technical Approach

### Payload struct

A single unexported struct in `internal/server/server.go` (the only caller):

```go
type triggerPayload struct {
    ClientID string   `json:"clientId"`
    Version  string   `json:"version"`
    Images   []string `json:"images"`
    Sizes    [][]int  `json:"sizes"`
    Formats  []string `json:"formats"`
}
```

After unmarshal:
1. Validate `clientId` non-empty.
2. Validate `images` non-empty.
3. Validate `formats` non-empty AND every entry ∈ `{png, jpg, jpeg, webp, avif}`.
4. Validate `sizes` (if present): every row is `[w, h]`, w ≥ 0, h ≥ 0.
5. Parse `version` (if present): `strconv.Atoi`. Default 3.

Any failure → `s.httpError(w, "...", http.StatusBadRequest)`.

### Worker API

```go
// internal/worker/worker.go
type BatchRequest struct {
    ClientID string
    Version  int
    Images   []string
    Sizes    [][]int  // empty/nil → use w.sizes
    Formats  []string // non-empty (precondition; validated at the server layer)
}

func (w *Worker) ProcessBatch(ctx context.Context, req BatchRequest) error
```

`ProcessBatch` loops `images × sizes × formats`, fetching each image's original once (re-used across all output sizes/formats for that image), resizing per `(size, format)`, applying the existing skip-existing + dual-write semantics per output. Returns nil unless a fatal error makes the whole batch un-processable (e.g. the input is empty after defaulting). Per-image / per-output failures are logged and skipped.

Output key template:

```go
fmt.Sprintf("%s/%d/images/products/%d/%d/%s.%s",
    req.ClientID, req.Version, width, height, origFilename, format)
```

### Removed code

- `Worker.ProcessProductImage` — replaced by `ProcessBatch` (the new method's body subsumes the old loop with parameterized clientId, version, and formats slice).
- `Worker.ProcessS3Event` — only caller was `handleWorkerTrigger`; that caller no longer exists in this shape.

### Files touched

- `internal/worker/worker.go` — add `BatchRequest`, add `ProcessBatch`, remove `ProcessProductImage` and `ProcessS3Event`.
- `internal/worker/worker_test.go` — replace `TestProcessProductImage_*` with `TestProcessBatch_*` coverage (happy path; sizes-default-to-env; multi-format fan-out; multi-image fan-out; skip-existing per-output; dual-write per output; per-output resize failure isolates).
- `internal/server/server.go` — `handleWorkerTrigger` rewrites to parse the new envelope and dispatch via `ProcessBatch`. Removes the legacy `payload.Key`-only path.
- `internal/server/server_test.go` — replace the legacy `TestWorkerTrigger` with a suite that covers the happy path + each 400 case + the 202-before-goroutine assertion.
- `README.md` — document the new payload shape under a new "Worker trigger" section.

### What is NOT touched

- `internal/resizer/*` — unchanged.
- `internal/accesslog/*` — unchanged (trigger requests still have no phase timings).
- `internal/s3/*` — unchanged.
- `cmd/image-proxy/main.go` — unchanged (no new env vars).
- `internal/types/types.go` — unchanged.

## Success Metrics

| Category | Metric | Target | Measurement |
|----------|--------|--------|-------------|
| Quality | All existing tests pass | 100% | `make test` (Alpine) |
| Quality | New unit + integration tests for `ProcessBatch` and the new trigger handler | ≥ 12 cases (one per AC plus a couple) | hand-review |
| Operational | After deploy, the catalog system makes 1 batch POST per upload instead of N | N=`sizes × formats` per upload | catalog system's POST log |
| Correctness | No `13/` hardcoded prefix in output S3 keys for any clientId other than 39 (sample audit) | 0 wrong-prefix outputs | one-off S3 list query after pre-warm |

## Stakeholders & Approvals

| Role | Name | Approval Required | Status |
|------|------|-------------------|--------|
| Owner | thomas@kasasagi.dk | Spec sign-off, payload-schema sign-off, deploy ordering with catalog system | [x] (single-maintainer) |

### Approval Gates

- [x] Spec approved
- [ ] Catalog system team has confirmed the new payload shape (clientId, images, sizes, formats, version) matches what they intend to send — **coordination gate**, not a code gate
- [ ] Deploy ordering agreed: catalog system migrates to the new payload **first**, image-proxy deploys second. Old `{"key":"..."}` returns 400 immediately on the new deploy.

## Risk Assessment

| Risk | Probability | Impact | Score | Mitigation |
|------|-------------|--------|-------|------------|
| Catalog system deploys after the proxy and sends the old payload → 400 storm | 3 | 4 | 12 | Coordinate deploy order; the new code rejects with a clear error body naming the new fields. A pre-deploy smoke check confirms the catalog system is on the new shape. |
| Caller sends a batch with `images × sizes × formats` so large the goroutine blocks libvips for minutes, increasing memory pressure | 2 | 3 | 6 | Sequential processing keeps peak memory bounded to one resize. Acceptable; if a batch becomes an operational problem, the follow-up is "cap batch size" or "queue rather than goroutine". |
| Resizer is called for the same `(image, size)` once per format, even though resizing-then-encoding could share intermediate state | 4 | 2 | 8 | Current resizer API takes the source bytes + format and produces encoded bytes. Refactoring to "decode once, encode N times" is out of scope; sticking with one Resize per output keeps the code straightforward. Tagged as a follow-up. |
| Skip-existing check for a size+format that already exists in cache is fine, but the same check against ORIGIN bucket in shadow/live mode requires reading origin too — adds origin-side existence calls that aren't there today (because the old worker only checked one client) | 2 | 2 | 4 | The existing dual-write skip-check from PR #2 already targets the cache client only; this track preserves that. Origin existence is not consulted. Documented in the test. |
| `clientId` in the payload becomes a tenant-impersonation vector if the trigger is reachable without auth | 3 | 4 | 12 | The trigger endpoint already has no auth (architecture.md §9 #7, existing risk). This track doesn't change that — but it makes the impersonation vector concrete (write to any tenant's prefix). Document explicitly. The mitigation is the same as the existing one: load-balancer-level IP allowlist for the trigger path. Out of scope to fix here. |
| `version` interpreted as string vs int across systems → 400s | 3 | 1 | 3 | Accept string and parse via `strconv.Atoi`; absent / empty → default 3. Numeric input would also work because `json.Unmarshal` into a `string` field accepts numeric tokens (Go stdlib: `cannot unmarshal number into Go struct field`). So actually `version: 3` (int) would error. Document explicitly: must be a string. |
| Existing `TestWorkerTrigger` keeps passing because of the test's loose assertions | 1 | 1 | 1 | Replace the test outright rather than extending it. |

## Deployment Strategy

Single PR. Catalog system migrates first (coordinated). Proxy deploy is a step function, not a canary.

### Rollback Plan

- **Trigger:** post-deploy logs show 400-rate spike on `/_/worker/trigger` indicating callers still on the old shape.
- **Process:** revert the merge commit, redeploy. The catalog system continues running with whichever payload shape it's on (no data shape changes, just the request envelope).
- **Data rollback:** N/A — failed worker invocations produce no thumbnails, but the read path's resize-on-miss continues to work; users see slightly more cache misses until pre-warm catches up.

### Monitoring

- After deploy, watch the access log's `response.status` distribution on `/_/worker/trigger`. Healthy = mostly 202, no 400 spike.
- The worker's success / failure log lines (`Saved thumbnail`, `Failed to save thumbnail`) are the per-output signal. No new log shape.

## Open Questions

| # | Question | Owner | Resolution |
|---|----------|-------|------------|
| Q1 | Should `version` be accepted as a JSON number in addition to string (to match the user's payload literal `"3"` vs. an integer)? | owner | Proposed: accept string ONLY. Caller sends `"3"`. Numeric input → 400. Simpler validator, predictable contract. |
| Q2 | Is `formats` constrained to the documented set `{png, jpg, jpeg, webp, avif}` or should `gif` also be accepted (the resizer's default fallback today is JPEG when an unknown format is passed)? | owner | Proposed: documented set only. `gif` is rejected. Mirrors the resizer's `switch` cases. |
| Q3 | Is the trigger endpoint going to gain auth in a follow-up? The clientId-impersonation vector becomes more concrete with this change. | owner | Acknowledged; out of scope for this track. The existing risk (architecture.md §9 #7) is unchanged. |

## Conversation Log

- **Decision:** Replace the legacy `{"key":"..."}` payload outright (no coexistence). Hardcoded `clientId=13` is removed entirely.
- **Decision:** `formats` is required. Absent or empty → 400. No default to single env FORMAT.
- **Decision:** `folder` stays hardcoded to `products`. Out of scope.
- **Decision:** Sequential processing within the detached goroutine. Parallel is a future track if batch throughput becomes a constraint.
- **Decision:** `version` is accepted as a JSON string (matches user's example payload literal). Empty / absent → defaults to 3. Numeric JSON tokens → 400 (caller must send `"3"`, not `3`).
- **Decision:** `ProcessProductImage` and `ProcessS3Event` are deleted, not preserved. Both only existed for the legacy trigger.
