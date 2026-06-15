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

# Plan: Multi-image / multi-format worker trigger envelope

| Field | Value |
|-------|-------|
| **Branch** | `extend-worker-payload` → none |
| **Commit** | `9ef919f` — feat: split origin/cache buckets + canary mode (off/shadow/live) (#2) |
| **Generated** | 2026-06-16T01:00:00Z |
| **Synced To** | `9ef919fc58db0e9452097b44533c62877e609c06` |

**Track ID:** extend-worker-payload
**Spec:** ./spec.md
**Status:** [x] Complete

## Overview

Two phases. Phase 1 builds `Worker.ProcessBatch` and deletes the legacy
methods (`ProcessProductImage`, `ProcessS3Event`). Phase 2 rewires the
`handleWorkerTrigger` HTTP handler to parse + validate the new envelope and
dispatch via `ProcessBatch`. Both phases land in one PR.

The worker fire-and-forget contract (architecture.md §2 I10) is preserved
end-to-end: validation runs synchronously before the 202, the goroutine
runs after.

## Phases

### Phase 1: Worker API — `BatchRequest` + `ProcessBatch`

**Goal:** Add a multi-image / multi-format batch entry point on the worker; delete the legacy single-key methods. Worker layer compiles and tests independently of the server layer.

**Verification:** `go test -race -v ./internal/worker/...` passes (note: server may still be temporarily broken on its `ProcessS3Event` reference — that's fixed in Phase 2).

#### Tasks

- [ ] **Task 1.1:** Define `BatchRequest` struct in `internal/worker/worker.go`:
  - `ClientID string`, `Version int`, `Images []string`, `Sizes [][]int`, `Formats []string`.
  - Document in a comment that `Sizes == nil || len(Sizes) == 0` means "fall back to the worker's env-configured sizes", and that `Formats` is a precondition (validated upstream).
- [ ] **Task 1.2:** Implement `(w *Worker) ProcessBatch(ctx context.Context, req BatchRequest) error`:
  - Effective sizes: `req.Sizes` if non-empty, else `w.sizes`.
  - Loop: for each image, fetch the original from `w.s3Client` once. If Get fails, log + skip image (per-image isolation, like today's per-size resize-error isolation).
  - Nested loops: per (image data, width, height, format) tuple, build `opts := types.ImageOptions{Width, Height, Version: req.Version, Format: format, Fit: "contain", IsAnimated: true}`, call `w.resizer.Resize(data, opts)`. Continue on per-output error.
  - Output key: `fmt.Sprintf("%s/%d/images/products/%d/%d/%s.%s", req.ClientID, req.Version, width, height, origFilename, format)`.
  - Reuse the existing skip-existing-cache check + dual-write logic from today's `ProcessProductImage` body verbatim (lines 78-106 of worker.go) but with `req.ClientID` / `req.Version` / `format` parameterized.
  - Return nil unconditionally — per-output failures are logged but never escalate to a batch error.
- [ ] **Task 1.3:** Remove `Worker.ProcessProductImage` and `Worker.ProcessS3Event` from `internal/worker/worker.go`. Also remove the now-unused `strings` import (was used by `ProcessS3Event` for the key routing).
- [ ] **Task 1.4:** Rewrite `internal/worker/worker_test.go` (the existing `TestProcessProductImage_*` suite is now obsolete):
  - Drop the in-package `mockResizer` (existing) but keep `mockS3Client`. Keep the `TestMain` log-silencing.
  - `TestProcessBatch_HappyPath_SingleImage_SingleSize_SingleFormat` — counts: 1 Get on origin, 1 Resize, 1 Put.
  - `TestProcessBatch_MultiImage` — 2 images × 1 size × 1 format → 2 Gets, 2 Resizes, 2 Puts. Assert output keys contain both image basenames.
  - `TestProcessBatch_MultiSize` — 1 image × 3 sizes × 1 format → 1 Get, 3 Resizes, 3 Puts.
  - `TestProcessBatch_MultiFormat` — 1 image × 1 size × 2 formats → 1 Get, 2 Resizes, 2 Puts. Output keys end in `.avif` and `.webp` (or whichever pair the test uses).
  - `TestProcessBatch_FullCartesian` — 2 images × 2 sizes × 2 formats → 2 Gets, 8 Resizes, 8 Puts. Output count = `len(images) × len(sizes) × len(formats)`.
  - `TestProcessBatch_SizesNil_UsesEnvDefaults` — `BatchRequest.Sizes` left empty; assert the worker's `w.sizes` (passed via `NewWorker`) drives output count.
  - `TestProcessBatch_ClientIDInOutputKey` — `ClientID = "39"`; assert every Put key starts with `39/`. Also assert no key starts with `13/` (regression guard against the old hardcode).
  - `TestProcessBatch_DualWritesWhenClientsDiffer` — distinct origin + cache mocks; assert both `putFunc`s called the expected number of times.
  - `TestProcessBatch_SkipExistingTargetsCache` — origin mock's `existsFunc` t.Errorf's if called; cache mock's returns `(true, nil)` → assert no Puts at all.
  - `TestProcessBatch_PerOutputResizeFailure_DoesNotAbortBatch` — resizer returns error for the 2nd size; assert sizes 1, 3, 4, ... all still Put. Output count = `total - 1`.
  - `TestProcessBatch_PerImageGetFailure_SkipsImageContinuesBatch` — origin `getFunc` returns error for image 2; assert image 1's outputs and image 3's outputs still appear, image 2's don't.
- [ ] **Task 1.5:** Run `go test -race -v ./internal/worker/...` — all new tests pass.

**Verification gate:**
- 10+ new tests pass with `-race`.
- `grep "13/" internal/worker/` returns no matches (hardcoded prefix gone).
- `grep "ProcessProductImage\|ProcessS3Event" internal/` returns matches only in `server.go` (the temporary dangling reference until Phase 2).

### Phase 2: Server handler — envelope parsing + dispatch + tests + ship

**Goal:** Rewrite `handleWorkerTrigger` to parse + validate the new envelope synchronously, then dispatch via `ProcessBatch` in the detached goroutine. Server layer compiles; full Alpine sweep green.

**Verification:** `make test` (Alpine) passes. Docker smoke covers both happy path and validation errors.

#### Tasks

- [ ] **Task 2.1:** In `internal/server/server.go`, rewrite `handleWorkerTrigger`:
  - Replace the existing `payload struct { Key string }` with `triggerPayload` carrying `ClientID`, `Version`, `Images`, `Sizes`, `Formats` (all JSON-tagged).
  - On `json.NewDecoder(r.Body).Decode(&payload)` failure → 400 with body `"Invalid request body"` (matches existing tone).
  - Validate in order; return 400 with a descriptive body on the first failure:
    - `clientId` non-empty.
    - `images` non-empty.
    - `formats` non-empty; every entry ∈ `{png, jpg, jpeg, webp, avif}` (a map literal at file scope works).
    - `sizes` (when present): every row has length 2 and both values ≥ 0.
    - `version` (when present and non-empty): parses via `strconv.Atoi` ≥ 0. Absent / empty → default `version = 3`.
  - Build `worker.BatchRequest{...}` and dispatch: `go func() { ctx := context.Background(); if err := s.worker.ProcessBatch(ctx, req); err != nil { log.Printf(...) } }()`.
  - Reply with `w.WriteHeader(http.StatusAccepted)` + `w.Write([]byte("Accepted"))`. Same as today.
- [ ] **Task 2.2:** Define `allowedFormats` at package scope in `server.go` as `map[string]bool{"png": true, "jpg": true, "jpeg": true, "webp": true, "avif": true}` so the validator is a constant-time lookup.
- [ ] **Task 2.3:** Replace `internal/server/server_test.go`'s `TestWorkerTrigger` with a tighter suite:
  - `TestWorkerTrigger_HappyPath` — full envelope, asserts 202 + the worker's `ProcessBatch` is reached (mock the worker via a function-pointer struct OR assert via a sentinel mockS3Client that records Puts).
  - `TestWorkerTrigger_MissingClientID` → 400.
  - `TestWorkerTrigger_MissingImages` → 400.
  - `TestWorkerTrigger_MissingFormats` → 400.
  - `TestWorkerTrigger_InvalidFormat` (e.g. `["tiff"]`) → 400.
  - `TestWorkerTrigger_InvalidSizesRow` (e.g. `[[200]]`) → 400.
  - `TestWorkerTrigger_NegativeSize` (e.g. `[[-1, 0]]`) → 400.
  - `TestWorkerTrigger_VersionInvalid` (e.g. `"abc"`) → 400.
  - `TestWorkerTrigger_VersionDefault` — `version` absent; check output keys contain `/3/`.
  - `TestWorkerTrigger_VersionExplicit` — `version: "2"`; check output keys contain `/2/`.
  - `TestWorkerTrigger_LegacyPayloadRejected` — `{"key":"foo"}` → 400.
  - `TestWorkerTrigger_FireAndForget` — handler returns before ProcessBatch finishes. Block ProcessBatch via a channel; verify the HTTP response was already 202 before unblocking.
- [ ] **Task 2.4:** Run `go vet ./...` and `gofmt -l ./...`. Both clean.
- [ ] **Task 2.5:** Run `make test` (Alpine). All 5 packages green.
- [ ] **Task 2.6:** Docker smoke. Build image. Run with fake AWS creds. POST the example payload from the spec; verify 202 + the JSON access log line shows `status=202`. POST the legacy `{"key":"foo"}`; verify 400. POST `{"clientId":"39","images":["foo.jpg"],"formats":["bogus"]}`; verify 400 with a body naming the invalid format.
- [ ] **Task 2.7:** Update `README.md` — add a "Worker trigger" subsection documenting the new payload (clientId required, images required, formats required, sizes optional with env default, version optional with default 3). Mention that the legacy `{"key": "..."}` shape is no longer accepted.
- [ ] **Task 2.8:** Commit Phase-1 changes as one commit (`feat: add Worker.ProcessBatch with multi-image / multi-format envelope`), Phase-2 changes as another (`feat: rewrite /_/worker/trigger to use ProcessBatch envelope`). Push branch. Open PR titled `feat: extend worker trigger to multi-image / multi-format batches`.

**Verification gate:**
- `make test` passes (Alpine).
- Docker smoke confirms 202 / 400 paths and the new payload roundtrips.
- README has the new payload documented.
- `git diff go.mod go.sum` empty.
- PR opened.

## Notes

- The single-PR delivery is intentional: Phase 1 leaves the codebase temporarily failing to build because `server.go` still references the removed `ProcessS3Event`. The two phases are sequential commits inside the same PR so that the merge boundary is buildable.
- After this lands, the natural follow-ups are: (a) **auth on the trigger** (the clientId-impersonation vector is now concrete; architecture.md §9 #7 was always the existing risk but it was less obvious when the worker was hardcoded to clientId=13), (b) **per-format optimization** — call the resizer once per `(image, size)` and emit N formats from a single decode-resize-encode pipeline (current code calls Resize per format, which decodes + resizes per call).
- The `version` field semantics: the spec keeps the "stringly typed" shape because the user's example sends `"3"`. JSON's number type would also work, but accepting both shapes complicates the validator. Acceptable trade-off; documented in spec Q1.
