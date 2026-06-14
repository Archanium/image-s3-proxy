---
project: "image-s3-proxy"
module: "root"
track_id: "split-origin-and-cache-buckets"
generated_by: "draft:new-track"
generated_at: "2026-06-13T17:05:00Z"
git:
  branch: "split-origin-and-cache-buckets"
  remote: "none"
  commit: "2718fcead03a1597b02f831ac8ea1175a7aa11eb"
  commit_short: "2718fce"
  commit_date: "2026-06-13 01:23:57 +0200"
  commit_message: "feat: structured access logs + Server-Timing header (#1)"
  dirty: false
synced_to_commit: "2718fcead03a1597b02f831ac8ea1175a7aa11eb"
---

# Plan: Split origin/cache buckets + single-GET cache-hit path

| Field | Value |
|-------|-------|
| **Branch** | `split-origin-and-cache-buckets` → none |
| **Commit** | `2718fce` — feat: structured access logs + Server-Timing header (#1) |
| **Generated** | 2026-06-13T17:05:00Z |
| **Synced To** | `2718fcead03a1597b02f831ac8ea1175a7aa11eb` |

**Track ID:** split-origin-and-cache-buckets
**Spec:** ./spec.md
**Status:** [x] Complete

## Overview

Three phases, each a self-contained commit-able milestone:

- **Phase 1 — Foundation:** Interface + s3 package changes. Drop the `tags` parameter, drop `defaultTags`/`SetDefaultTags`, replace string-matching error classification with typed errors. Update worker tests + s3 tests. Nothing in `server` or `main` yet.
- **Phase 2 — Wire it up:** `main.go` reads `CACHE_*` env vars and constructs the second client. `server.NewServer` takes two clients. `ServeHTTP` uses single-GET against cacheClient. `handleResize`/`handleFile` split reads vs writes. Worker `destS3Client` is now populated. `server_test.go` covers single-client mode, split-mode hit RTT, split-mode resize phases, typed-error miss-vs-fail-open.
- **Phase 3 — Verify + ship:** Full test sweep (Alpine + Debian), live Docker smoke in single-client mode and split-mode (against a sentinel sub-bucket of the same HOS account to keep credentials simple), README update for env vars + deprecation, PR.

Each phase is committed as a single conventional commit per the workflow doc; impl + test grouped per file (matches the repo idiom established in the access-logs track).

## Phases

### Phase 1: Foundation — `internal/types` and `internal/s3` refactor

**Goal:** Drop Tagging from the public S3 surface, switch to AWS SDK v2 typed-error classification, and remove the now-dead `defaultTags`/`SetDefaultTags` plumbing. All changes are local to `internal/types`, `internal/s3`, and the package tests.

**Verification:** `go build ./internal/types/... ./internal/s3/... ./internal/worker/...` clean; `go test -race -v ./internal/types/... ./internal/s3/... ./internal/worker/...` passes (worker is in scope because its mocks depend on the Put signature).

#### Tasks

- [ ] **Task 1.1:** In `internal/types/types.go`, change `S3Client.Put` signature from `Put(ctx, key, data, contentType, tags map[string]string) error` to `Put(ctx, key, data, contentType) error`. Storage interface (currently unused, dead code per architecture.md §9 #3) — leave as-is or delete; spec defers that cleanup.
- [ ] **Task 1.2:** In `internal/s3/s3.go`:
  - Remove the `defaultTags map[string]string` field from `Client`.
  - Remove the `SetDefaultTags` method.
  - Change `Put` signature to `Put(ctx, key, data, contentType) error` and remove all `Tagging` header construction + URL-escape logic (current `s3.go:154-163`).
  - Replace string-matching error classification in `Exists` (current `s3.go:84`) and `Get` (current `s3.go:113,118`) with `errors.As` against `*types.NoSuchKey` and `*types.NotFound` from `github.com/aws/aws-sdk-go-v2/service/s3/types`. Add a small unexported helper `isNotFound(err error) bool`.
  - Update the lazy-migration copy-back call in `Get` to use the new tag-less `Put` (the migrated tags concept goes away).
- [ ] **Task 1.3:** In `internal/s3/s3_test.go`:
  - Remove `Migrated=true` tag assertions from existing tests (the `TestFallback`-family tests had `if params.Tagging == nil || *params.Tagging != "Migrated=true"` — drop).
  - Add `TestExists_TypedNoSuchKey` and `TestGet_TypedNoSuchKey` — confirm that returning a real `*types.NoSuchKey` from the mock surfaces as the documented behavior (Exists → `(false, nil)`, Get → fallback consult or `(nil, "", error)`).
  - Add `TestExists_NonNotFoundError` and `TestGet_NonNotFoundError` — confirm a synthetic 5xx error propagates (no silent misclassification).
- [ ] **Task 1.4:** In `internal/worker/worker.go`: change every `c.Put(... tags ...)` (`worker.go:93`) to the new tag-less signature. The `tags map[string]string` parameter on `NewWorker` (`worker.go:29`) becomes unused — remove from the constructor signature.
- [ ] **Task 1.5:** In `internal/worker/worker_test.go`: update `mockS3Client.putFunc` and every test that asserted on the `tags` argument. Drop the assertions on tag values; preserve the assertion that Put was called with the correct (bucket, key, data, contentType).
- [ ] **Task 1.6:** Run `go vet ./internal/types/... ./internal/s3/... ./internal/worker/...` and `go test -race -v ./internal/s3/... ./internal/worker/...`. All pass.

**Verification gate:**
- `s3.Client.Put` accepts no `tags` parameter anywhere in the call graph below `server`.
- `worker_test.go` and `s3_test.go` pass with `-race`.
- No reference to `defaultTags` or `SetDefaultTags` remains (`grep -r defaultTags internal/`).

### Phase 2: Wire it up — `internal/server` + `cmd/image-proxy`

**Goal:** Two-client wiring in `main.go`, two-client storage in `Server`, single-GET cache-hit path, origin-reads-vs-cache-writes split in handlers, worker destS3Client populated. Integration tests cover both single-client and split-client modes.

**Verification:** `make test` and `make test-debian` both pass. `gofmt -l` clean.

#### Tasks

- [ ] **Task 2.1:** Introduce `CacheMode` type in `internal/server/server.go`:
  - Unexported `cacheMode int` with constants `cacheModeOff`, `cacheModeShadow`, `cacheModeLive`.
  - Unexported parser `parseCacheMode(s string) (cacheMode, error)` — accepts case-insensitive `"off"`, `"shadow"`, `"live"`; empty → `cacheModeOff`; any other value → error.
  - Exported `NewServerWithMode(originClient, cacheClient types.S3Client, mode cacheMode, sizes [][]int, format string) *Server` is the new constructor. Keep the existing `NewServer(originClient, cacheClient, sizes, format)` as a thin wrapper that calls with `cacheModeOff` — preserves call sites that don't care about the mode (mostly tests).
  - Store `mode cacheMode` on the `Server` struct.
- [ ] **Task 2.2:** Refactor `internal/server/server.go` struct and constructor body:
  - Replace `s3Client types.S3Client` field with `originClient, cacheClient types.S3Client` on the `Server` struct.
  - Internally, construct the worker with `worker.NewWorker(originClient, cacheClient, resizer, sizes, format, false)` (tag-less per Phase 1). The worker is constructed even when `mode == cacheModeOff` — in that case `originClient == cacheClient` so the worker's existing `destS3Client != nil` branch naturally short-circuits to single-client behavior.
  - The `s.time` helper introduced by the access-logs track stays.
- [ ] **Task 2.3:** Add the read-source dispatch helper `(s *Server).effectiveReadClient(r *http.Request) types.S3Client`:
  - If `s.mode == cacheModeOff`: return `s.originClient` (same as cache; doesn't matter).
  - Else: start with `useCache := s.mode == cacheModeLive`. If header `X-Use-Cache` is exactly `"true"` set `useCache = true`; if exactly `"false"` set `useCache = false`. Any other header value is ignored.
  - Return `s.cacheClient` if `useCache` else `s.originClient`.
- [ ] **Task 2.4:** Refactor `ServeHTTP`:
  - Replace the existing `Exists` + conditional `Get` block (currently lines 64-93 after the access-logs merge) with a single call to `s.effectiveReadClient(r).Get(ctx, key)` wrapped in `s.time(ctx, "s3-get", ...)`. On success: set Content-Type + Cache-Control, write body, return. On `errors.As(err, *types.NoSuchKey)` / `*types.NotFound`: fall through to regex matching with no log line. On other error: `log.Printf("cache client error for %s: %v", key, err)` then fall through (preserving fail-open).
  - In the `folderImageRegex` branch (current ~line 113): use the same single-GET pattern against `s.effectiveReadClient(r)`.
- [ ] **Task 2.5:** Add the dual-write helper `(s *Server).putBoth(ctx context.Context, key string, data []byte, contentType string)`:
  - If `s.mode == cacheModeOff`: a single timed `s3-put` call against `s.originClient` (same as cache; same as today). Preserves the bare `s3-put` phase name for off-mode dashboards.
  - Else: depending on mode, run two timed calls. In `cacheModeShadow`: origin first as `s3-put-origin`, then cache as `s3-put-cache`. In `cacheModeLive`: cache first as `s3-put-cache`, then origin as `s3-put-origin`. Each write is timed via the `s.time` helper. Failures are logged with `log.Printf("dual-write %s failed for %s: %v", side, key, err)` but do not abort the function — the second write is still attempted. Function returns nothing (writes are best-effort; caller already has the data to serve).
- [ ] **Task 2.6:** Refactor `handleResize` and `handleFile` to use `s.putBoth`:
  - In `handleResize`: all `s.s3Client.Get` calls become `s.originClient.Get` (candidates for the original); the final cache-back `s.s3Client.Put` becomes a `s.putBoth(ctx, key, resizedData, contentType)` call.
  - In `handleFile`: `Get` becomes `s.originClient.Get`; cache-back `Put` becomes `s.putBoth(...)`.
- [ ] **Task 2.7:** In `cmd/image-proxy/main.go`:
  - Read `CACHE_MODE` env (default empty → `cacheModeOff`). Parse via `server.ParseCacheMode` (exported wrapper for the unexported parser — or just inline the parse here since `main.go` is the one caller).
  - Read `CACHE_BUCKET`, `CACHE_S3_ENDPOINT`, `CACHE_AWS_ACCESS_KEY_ID`, `CACHE_AWS_SECRET_ACCESS_KEY`, `CACHE_AWS_REGION` env vars (group them next to the existing `OLD_*` reads for symmetry).
  - Sanity: if `CACHE_MODE != off` && `CACHE_BUCKET == ""`: `log.Fatal("CACHE_MODE=%s requires CACHE_BUCKET to be set", modeStr)`.
  - Build `originClient` exactly as `s3Client` is built today.
  - Default `cacheClient := originClient`. If `mode != cacheModeOff`: build a second `*s3.Client` against the cache config and assign `cacheClient = thatClient`. Else if `CACHE_BUCKET != ""`: log warning `CACHE_BUCKET is set but CACHE_MODE is off — cache client will not be constructed`.
  - Read `IMAGE_TAGS`; if non-empty, log `log.Printf("IMAGE_TAGS is deprecated and ignored — HOS and R2 do not implement S3 Tagging APIs")` and discard.
  - Construct `server.NewServerWithMode(originClient, cacheClient, mode, sizes, format)`.
  - The `OLD_S3_*` fallback block continues to apply to the **origin** client (`originClient.SetFallback(...)`) — unchanged in role.
  - The `upstreamHost` value passed to `accesslog.Middleware` should reflect the **cache** bucket/endpoint when `mode != off` (that's where the dominant write traffic lands); when off, fall back to the existing value. Document the choice in a one-line comment.
- [ ] **Task 2.8:** In `internal/server/server_test.go`, update existing tests:
  - Update `mockS3Client.putFunc` to the new tag-less signature.
  - Update every `NewServer(...)` constructor in existing tests — easiest pattern: pass the same `s3` mock as both arguments (`NewServer(s3, s3, nil, "")`) — this is the off-mode fixture and exercises every existing test case with no behavior change.
  - Existing `TestServeHTTP_ExistingFile`, `TestServeHTTP_Resize`, `TestWorkerTrigger`, etc. should keep passing with the single-mock-as-both-args fixture.
- [ ] **Task 2.9:** In `internal/server/server_test.go`, add mode/header tests:
  - `TestServeHTTP_OffMode_NoModeKnobsOrHeaderEffect` — `NewServer(s3, s3, ...)` (off mode), with and without `X-Use-Cache: true` header — both behave identically.
  - `TestServeHTTP_ShadowMode_DefaultReadFromOrigin` — distinct origin/cache mocks, `NewServerWithMode(..., cacheModeShadow, ...)`, request hits a key the **origin** mock has, assert origin's `getFunc` called exactly once and cache's `getFunc` never called.
  - `TestServeHTTP_ShadowMode_HeaderForceReadFromCache` — same setup as above, but request has `X-Use-Cache: true`; assert cache's `getFunc` was called and origin's was not.
  - `TestServeHTTP_LiveMode_DefaultReadFromCache` — `NewServerWithMode(..., cacheModeLive, ...)`, cache mock has the key, assert cache's `getFunc` called and origin's not.
  - `TestServeHTTP_LiveMode_HeaderForceReadFromOrigin` — same as above but `X-Use-Cache: false`; assert origin's `getFunc` called.
  - `TestServeHTTP_NoSuchKey_CleanFallThrough` — effective-read mock returns `*types.NoSuchKey`, request falls through with no error log line.
  - `TestServeHTTP_NonNotFoundError_FallsThroughWithLog` — effective-read mock returns a synthetic 5xx, request falls through and a log line containing `cache client error` is emitted.
- [ ] **Task 2.10:** In `internal/server/server_test.go`, add dual-write tests:
  - `TestHandleResize_ShadowMode_DualWritesOriginFirst` — distinct origin/cache mocks, resize request that misses. Assert both `putFunc`s were called exactly once. Use a per-mock atomic counter recorded into a shared `callOrder []string` to assert origin Put was recorded before cache Put.
  - `TestHandleResize_LiveMode_DualWritesCacheFirst` — same as above but mode=live; assert cache Put was recorded first.
  - `TestHandleResize_DualWriteCacheFailure_StillSucceeds` — cache mock's `putFunc` returns an error; assert response is still 200 + resized body, and the origin Put still happened, and a log line containing `dual-write cache failed` is emitted.
  - `TestHandleResize_DualWriteOriginFailure_StillSucceeds` — symmetric case for origin failing.
  - `TestServeHTTP_ServerTimingPhases_DualWriteMode` — wrap server in middleware (parallel to the existing access-log integration tests), resize request, assert `Server-Timing` header contains both `s3-put-cache` and `s3-put-origin` and does NOT contain a bare `s3-put`.
  - `TestServeHTTP_ServerTimingPhases_OffMode` — same shape but mode=off; assert `Server-Timing` contains bare `s3-put` and no `s3-put-cache`/`s3-put-origin`.
- [ ] **Task 2.11:** In `internal/worker/worker.go` / `worker_test.go`: confirm the worker's `ProcessProductImage` dual-writes when `destS3Client != s3Client`. The existing `destS3Client != nil` branch is the right shape but currently the worker passes the result to **only** `destS3Client`. Update to write to **both** when both are non-nil and distinct. Add `TestProcessProductImage_DualWritesWhenClientsDiffer`.
- [ ] **Task 2.12:** Run `make test` (Alpine) and `make test-debian`. Both pass with no regressions on existing tests.
- [ ] **Task 2.13:** Run `gofmt -l ./...` and `go vet ./...`. Both clean.

**Verification gate:**
- All existing tests pass under both Alpine and Debian Docker images.
- New split-mode tests pass.
- `go.mod` and `go.sum` are unchanged (`git diff go.mod go.sum` empty).
- `grep -rn "Tagging:" internal/ cmd/` returns no matches.

### Phase 3: Verify + ship

**Goal:** Live smoke in both modes, README update, PR.

**Verification:** Single-client smoke (`CACHE_BUCKET` unset) returns identical responses to the pre-track behavior; split-client smoke (sentinel two-bucket setup) verifies cache-hit single-RTT and split-mode wiring on a real binary.

#### Tasks

- [ ] **Task 3.1:** Run the same Docker smoke as the access-logs track but in `off` mode (`CACHE_MODE` unset). Confirm cache-hit `Server-Timing` now contains a single `s3-get` entry (no `s3-exists`). Confirm `IMAGE_TAGS` deprecation log appears at startup when set. Confirm the bare `s3-put` phase name appears in resize-miss Server-Timing.
- [ ] **Task 3.2:** Run a `shadow`-mode smoke. Two distinct bucket names against the same fake AWS endpoint (the proxy will get 403s on writes — wiring-only smoke). Confirm:
  - With `CACHE_MODE=shadow` + `CACHE_BUCKET=cache-smoke`, default cache-hit attempts target the **origin** bucket (visible in stderr `Fetching from S3: bucket=<origin>,...`).
  - With `X-Use-Cache: true` header on the same URL, the attempt targets the **cache** bucket.
  - Resize-path requests show dual-write log lines for both buckets (`bucket=<origin>` and `bucket=cache-smoke` in stderr) and Server-Timing contains both `s3-put-origin` and `s3-put-cache`.
  - The `s3-put-origin` phase appears before `s3-put-cache` in Server-Timing (order observable via the header).
- [ ] **Task 3.3:** Run a `live`-mode smoke. Same two-bucket setup but `CACHE_MODE=live`. Confirm:
  - Default cache-hit attempts target the **cache** bucket.
  - `X-Use-Cache: false` header re-routes the attempt to the **origin** bucket.
  - Server-Timing on a resize miss contains `s3-put-cache` before `s3-put-origin` (order flipped vs shadow).
- [ ] **Task 3.4:** Sanity-check the mode-validation error. Start the proxy with `CACHE_MODE=live` and `CACHE_BUCKET` unset — must exit with a clear log message naming both env vars. Start with `CACHE_MODE=off` and `CACHE_BUCKET=foo` — must log the "set-but-ignored" warning and start successfully.
- [ ] **Task 3.5:** Update `README.md`:
  - Add the `CACHE_MODE`, `CACHE_BUCKET`, `CACHE_S3_ENDPOINT`, `CACHE_AWS_ACCESS_KEY_ID`, `CACHE_AWS_SECRET_ACCESS_KEY`, `CACHE_AWS_REGION` env vars under "Environment Variables" — `CACHE_MODE` listed first with the three values explained.
  - Add a "Read-source override" subsection documenting the `X-Use-Cache: true|false` request header.
  - Mark `IMAGE_TAGS` as deprecated.
  - Add a 5-7 line "Storage backends" section explaining the origin/cache split, the three modes, and the canary migration sequence (off → shadow → test with header → live).
- [ ] **Task 3.6:** Resolve **Open Question Q1** (does anything outside the proxy actually read S3 tags from `IMAGE_TAGS`?). If the answer is "no" (expected — HOS dropped them silently), nothing else to do. If "yes", surface to the user as a blocker and stop here.
- [ ] **Task 3.7:** Manual deploy-checklist (same items as the access-logs track):
  - Cache-Control: `max-age=31536000` on 2xx, `max-age=30` on errors — preserved.
  - Worker fire-and-forget contract (I10) preserved.
  - libvips lifecycle untouched (no edits to `resizer.go`).
  - Origin-side `OLD_S3_BUCKET` fallback still works.
  - go.mod unchanged.
- [ ] **Task 3.8:** Commit Phase-1 changes (`refactor: drop S3 Tagging + typed-error classification`) and Phase-2 changes (`feat: split origin/cache S3 clients + canary modes`) as separate commits. Push branch and open PR. Title: `feat: split origin/cache buckets + canary mode (off/shadow/live)`. PR body includes the rollout-sequence section from spec.md and notes that the PR is shippable in `off` mode (no-op) and only requires `CACHE_MODE=shadow` + the cache credentials after R2 provisioning.

**Verification gate:**
- All four smoke scenarios (off / shadow / live / mode-validation) produced expected stderr logs and response headers.
- README env-var table is complete (modes + header documented).
- Q1 resolved.
- PR opened.

## Notes

- **Coupling note for reviewers:** Phase 1's signature changes look like a bigger refactor than the feature really is. They are mechanical (`Put` loses a parameter, every caller updates) and they are required by the Tagging removal. Reviewing Phase 1 as a pure refactor and Phase 2 as the actual feature is the recommended split. That's why they ship as two distinct commits even though the PR is single.
- **What the hot path looks like after Phase 2:** see spec.md §"Request lifecycle (after change)". The user-visible change on a cache hit is: `Server-Timing: s3-get;dur=...` instead of `Server-Timing: s3-exists;dur=...,s3-get;dur=...`. Dashboards keyed on `s3-exists` presence will see fewer entries (zero on cache hits). Not a regression; flag in deploy notes.
- **Follow-up tracks (already enumerated in spec.md Non-Goals), in expected priority order after this lands:**
  1. Parallel candidate-key GETs in `handleResize` (`s3-get` p99 on the miss path).
  2. Fire-and-forget cache-back PUT to R2 (removes `s3-put` from user-perceived latency).
  3. AWS SDK v2 `http.Transport` tuning (`MaxIdleConnsPerHost`, etc.) — verify default really hurts before tuning.
  4. Optional: startup `HeadBucket` sanity check (risk row 7 in spec) — catches typo-driven misconfigurations early.
