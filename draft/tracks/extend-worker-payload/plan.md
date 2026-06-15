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

- [x] **Task 1.1:** `BatchRequest{ClientID, Version, Images, Sizes, Formats}` defined in `internal/worker/worker.go`. (de1ce7e)
- [x] **Task 1.2:** `(w *Worker).ProcessBatch` implemented. Uses unexported `processOutput` helper for the per-(image, size, format) leaf. Effective sizes default to `w.sizes` when `req.Sizes` is empty/nil. (de1ce7e)
- [x] **Task 1.3:** `Worker.ProcessProductImage` and `Worker.ProcessS3Event` removed; unused `strings` import dropped. (de1ce7e)
- [x] **Task 1.4:** `internal/worker/worker_test.go` rewritten — 11 tests cover all the spec'd cases including the `13/`-prefix regression guard. (de1ce7e)
- [x] **Task 1.5:** `go test -race -v ./internal/worker/...` — 11/11 pass.

**Verification gate:**
- 10+ new tests pass with `-race`.
- `grep "13/" internal/worker/` returns no matches (hardcoded prefix gone).
- `grep "ProcessProductImage\|ProcessS3Event" internal/` returns matches only in `server.go` (the temporary dangling reference until Phase 2).

### Phase 2: Server handler — envelope parsing + dispatch + tests + ship

**Goal:** Rewrite `handleWorkerTrigger` to parse + validate the new envelope synchronously, then dispatch via `ProcessBatch` in the detached goroutine. Server layer compiles; full Alpine sweep green.

**Verification:** `make test` (Alpine) passes. Docker smoke covers both happy path and validation errors.

#### Tasks

- [x] **Task 2.1:** `handleWorkerTrigger` rewritten with the new `triggerPayload` envelope and full validation chain. (0feea1f)
- [x] **Task 2.2:** `allowedFormats` at package scope, validates `{png, jpg, jpeg, webp, avif}` with a constant-time lookup. (0feea1f)
- [x] **Task 2.3:** `internal/server/server_test.go` — replaced `TestWorkerTrigger` with 12 new cases. (0feea1f)
- [x] **Task 2.4:** `gofmt -l ./...` and `go vet ./internal/...` both clean (drive-by gofmt on `worker_test.go`).
- [x] **Task 2.5:** `make test` (Alpine) — 5/5 packages green.
- [x] **Task 2.6:** Docker smoke confirmed: happy-path → 202; legacy `{"key":"..."}` → 400 "clientId is required"; invalid `["bogus"]` format → 400 with the value named. Worker stderr shows `Worker: batch start — clientId=39 version=3 images=1 sizes=1 formats=2 (total outputs=2)` and per-image Get-failure isolation (403 from fake AWS creds was logged and skipped, batch did not crash).
- [x] **Task 2.7:** README "Worker trigger" section + env-var notes updated. (71a30ef)
- [x] **Task 2.8:** Pushed `extend-worker-payload`; opened PR #4 — https://github.com/Archanium/image-s3-proxy/pull/4

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
