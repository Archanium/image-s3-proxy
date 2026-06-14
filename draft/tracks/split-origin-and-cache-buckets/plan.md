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

- [x] **Task 1.1:** `internal/types/types.go` — `S3Client.Put` signature now `Put(ctx, key, data, contentType) error`; `Storage.Put` updated for symmetry. (4b89142)
- [x] **Task 1.2:** `internal/s3/s3.go` — `Put` is tag-less; `defaultTags`/`SetDefaultTags` removed; `isNotFound` helper uses typed errors with string-match fallback for HOS-style providers; fallback-bucket Get/Exists/copy-back use the new classifier. (4b89142)
- [x] **Task 1.3:** `internal/s3/s3_test.go` — `TestFallback` Tagging assertion replaced with "Tagging must not be set"; new typed-error tests `TestExists_TypedNoSuchKey`, `TestExists_TypedNotFound`, `TestExists_NonNotFoundErrorPropagates`, `TestGet_TypedNoSuchKey_NoFallback`, `TestGet_TypedNoSuchKey_WithFallback`, `TestPut_NoTaggingHeader`. (4b89142)
- [x] **Task 1.4:** `internal/worker/worker.go` — `NewWorker` loses `tags` param; `Worker.tags` field removed; `Put` call is tag-less. (1a46504)
- [x] **Task 1.5:** `internal/worker/worker_test.go` — `mockS3Client.putFunc` signature updated; tag assertions dropped. (1a46504)
- [x] **Task 1.6:** `go test -race -v ./internal/s3/... ./internal/worker/...` — 14 tests pass with `-race`; `go vet` clean. Note: `internal/server` is expected to fail to build at this point (still passes `s.tags` to `Put`); fixed in Task 2.6.

**Verification gate:**
- `s3.Client.Put` accepts no `tags` parameter anywhere in the call graph below `server`.
- `worker_test.go` and `s3_test.go` pass with `-race`.
- No reference to `defaultTags` or `SetDefaultTags` remains (`grep -r defaultTags internal/`).

### Phase 2: Wire it up — `internal/server` + `cmd/image-proxy`

**Goal:** Two-client wiring in `main.go`, two-client storage in `Server`, single-GET cache-hit path, origin-reads-vs-cache-writes split in handlers, worker destS3Client populated. Integration tests cover both single-client and split-client modes.

**Verification:** `make test` and `make test-debian` both pass. `gofmt -l` clean.

#### Tasks

- [x] **Task 2.1:** `CacheMode` type + constants + `ParseCacheMode` parser + `String()` method. Exported so `main.go` can use it. (4886940)
- [x] **Task 2.2:** Server struct rewired: `originClient`/`cacheClient`/`mode` fields, worker constructed inline. (4886940)
- [x] **Task 2.3:** `effectiveReadClient(r)` helper: dispatches on mode + X-Use-Cache header (`true`/`false` only). (4886940)
- [x] **Task 2.4:** `ServeHTTP` single-GET path via effective read client; typed-error miss vs `cache client error` log + fall-through. folderImageRegex normalized-key branch likewise. (4886940)
- [x] **Task 2.5:** `putBoth(ctx, key, data, contentType)` — off → bare `s3-put`; shadow → `s3-put-origin` then `s3-put-cache`; live → reverse. Per-side failures log `dual-write {side} failed` and never abort the second write. (4886940)
- [x] **Task 2.6:** `handleResize`/`handleFile` read via `originClient`, write via `putBoth`. Confirmed Put-before-Write order preserved. (4886940)
- [x] **Task 2.7:** `cmd/image-proxy/main.go`:
  - `CACHE_MODE` parsed via `server.ParseCacheMode`; mismatched config (mode != off without `CACHE_BUCKET`) is a `log.Fatal`. `CACHE_BUCKET` set with `CACHE_MODE=off` logs a warning and ignores.
  - `originClient` built unchanged; `OLD_S3_*` fallback stays on origin.
  - `cacheClient` constructed when mode != off, else aliased to originClient.
  - `IMAGE_TAGS` becomes a logged-and-ignored deprecation.
  - `upstreamHost` reflects the cache endpoint/bucket in split mode. (536a5e8)
- [x] **Task 2.8:** Existing tests updated — `mockS3Client.putFunc` is tag-less; all `NewServer(...)` call sites use the 3-arg signature (origin == cache as the single-mock fixture). (4886940)
- [x] **Task 2.9:** Added 7 mode/header tests covering `off` (no-op), `shadow` (default-origin + header-force-cache), `live` (default-cache + header-force-origin), `NoSuchKey` clean fall-through, and non-not-found logs-and-falls-through. (4886940)
- [x] **Task 2.10:** Added 6 dual-write tests covering shadow-order, live-order, cache-failure, origin-failure, and per-mode Server-Timing phase distinctions (`s3-put-cache` + `s3-put-origin` in non-off modes, bare `s3-put` only in off). (4886940)
- [x] **Task 2.11:** Worker `ProcessProductImage` now dual-writes (origin then cache) when `destS3Client != s3Client`; per-side failures log independently. Exists-check targets the cache (the bucket being populated). Added `TestProcessProductImage_DualWritesWhenClientsDiffer` and `TestProcessProductImage_ExistsCheckTargetsDestWhenSplit`. Replaced the old `TestProcessProductImage_DestClient` which asserted single-target writes. (536a5e8)
- [x] **Task 2.12:** `make test` (Alpine) passes; 5/5 packages green; no regressions on the 25+ existing tests. Worker now has 6 tests (was 5).
- [x] **Task 2.13:** `gofmt -l` clean (drive-by fix on `internal/accesslog/log.go` rolled into commit 536a5e8). `go vet` clean on accesslog/types/s3/server/worker.

**Verification gate:**
- All existing tests pass under both Alpine and Debian Docker images.
- New split-mode tests pass.
- `go.mod` and `go.sum` are unchanged (`git diff go.mod go.sum` empty).
- `grep -rn "Tagging:" internal/ cmd/` returns no matches.

### Phase 3: Verify + ship

**Goal:** Live smoke in both modes, README update, PR.

**Verification:** Single-client smoke (`CACHE_BUCKET` unset) returns identical responses to the pre-track behavior; split-client smoke (sentinel two-bucket setup) verifies cache-hit single-RTT and split-mode wiring on a real binary.

#### Tasks

- [x] **Task 3.1:** Off-mode Docker smoke (CACHE_MODE unset). Confirmed: `IMAGE_TAGS is deprecated...` startup log; startup logs `cache mode: off`; cache-hit + resize requests emit only `s3-get` in Server-Timing (no `s3-exists`); JSON access log schema unchanged.
- [x] **Task 3.2:** Shadow-mode smoke. Confirmed: startup logs `Initializing cache S3 client for bucket: smoke-cache (mode=shadow)`; default reads target `bucket=smoke-origin`; `X-Use-Cache: true` header redirects read to `bucket=smoke-cache`; resize-path candidate Gets all go to origin; both buckets are wired and reachable.
- [x] **Task 3.3:** Live-mode smoke. Confirmed: startup logs `cache mode: live`; default reads target `bucket=smoke-cache`; `X-Use-Cache: false` header redirects read to `bucket=smoke-origin`. Per-side Server-Timing phase order in dual-write covered by unit tests (live: cache first, shadow: origin first).
- [x] **Task 3.4:** Mode-validation smoke. Confirmed three error/warning paths: `CACHE_MODE=live` without `CACHE_BUCKET` → `log.Fatal("CACHE_MODE=live requires CACHE_BUCKET to be set")`. `CACHE_MODE=off` with `CACHE_BUCKET=foo` → warning + starts. `CACHE_MODE=bogus` → `log.Fatal("invalid CACHE_MODE: invalid CACHE_MODE \"bogus\" (expected off|shadow|live)")`.
- [x] **Task 3.5:** README updated with `CACHE_MODE` + all `CACHE_*` env vars, a "Storage backends" section explaining off/shadow/live + migration sequence, a "Read-source override" subsection on `X-Use-Cache`, and `IMAGE_TAGS` marked deprecated with the rationale.
- [x] **Task 3.6:** Q1 resolved (carried over): IMAGE_TAGS was silently no-op on HOS for the lifetime of the deployment. Nothing outside the proxy can be relying on them. Deprecation is safe.
- [x] **Task 3.7:** Deploy-checklist verified: Cache-Control `max-age=31536000`/`max-age=30` preserved; worker fire-and-forget (I10) unchanged; libvips lifecycle untouched (no edits to resizer.go); origin-side `OLD_S3_BUCKET` fallback still wires via `originClient.SetFallback`; `git diff go.mod go.sum` empty.
- [ ] **Task 3.8:** Push branch and open PR.

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
