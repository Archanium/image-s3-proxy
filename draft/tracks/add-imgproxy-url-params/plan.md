---
project: "image-s3-proxy"
module: "root"
track_id: "add-imgproxy-url-params"
generated_by: "draft:new-track"
generated_at: "2026-08-22T09:44:08Z"
git:
  branch: "add-imgproxy-url-params"
  remote: "none"
  commit: "8258427a9bf7912be133749d1286c53bea4723b4"
  commit_short: "8258427"
  commit_date: "2026-06-16 11:10:30 +0200"
  commit_message: "feat: optional bearer-token auth on POST /_/worker/trigger (#5)"
  dirty: false
synced_to_commit: "8258427a9bf7912be133749d1286c53bea4723b4"
---

# Plan: imgproxy-style processing options + signed URLs

**Track ID:** add-imgproxy-url-params
**Spec:** ./spec.md
**Status:** [x] Complete

## Overview

Three phases, ordered so that risk drops monotonically. Phase 1 and Phase 2 add
new code that nothing calls yet — neither can regress production. Phase 3 is the
only phase that touches the request path, and its single shared-code edit
(extracting the candidate-key ladder) is verified by the untouched legacy suite.

Phases 1 and 2 are independently testable; Phase 3 depends on both.

Commit granularity follows the repo idiom: implementation and its tests together,
one commit per file-scoped concern, conventional prefixes.

---

## Phase 1: Pure packages — parsing, canonicalisation, signing  [x] Complete

**Goal:** `internal/procopts` and `internal/urlsign` are complete, exhaustively
tested, and importable. No existing file is modified.

**Verification:** `go test -cover ./internal/procopts/... ./internal/urlsign/...`
runs on the host without libvips and reports > 95% coverage. Nothing else in the
tree changes, so `make test` is unaffected.

### Tasks

- [x] **Task 1 (2f3eae4):** `internal/procopts/options.go` — `Options` struct plus the
      option table (long name, aliases, arity, argument parser) and the supporting
      value types for gravity, crop geometry, and colour. One table entry per
      wave-1 option from spec F4.
- [x] **Task 2 (2f3eae4):** `internal/procopts/parse.go` — `Parse(segments []string)`
      returning options, the source tail, and a typed error. Splits options from
      source at the first segment not matching `^[a-z_]+:`; expands the compound
      `rs` / `s` forms; every error names the offending option.
- [x] **Task 3 (371e5f8):** `internal/procopts/canonical.go` — `Options.Canonical()`
      implementing the six normalisation steps in spec F9, including the `_`
      placeholder for an empty option set.
- [x] **Task 4 (2f3eae4):** `internal/procopts/options_test.go` — table-driven coverage of
      every option via its long name and each alias; both boolean spellings; hex
      `fff` / `ffffff` and `R:G:B` colours; gravity types including `sm` and
      `fp:x:y`; crop in relative and absolute form.
- [x] **Task 5 (2f3eae4):** `internal/procopts/parse_test.go` — the option/source split
      (including a source whose first segment contains no colon), wrong arity,
      unknown option name, unknown resizing type, bad hex, non-numeric dimension.
      Each case asserts the error message names the failing option.
- [x] **Task 6 (371e5f8):** `internal/procopts/canonical_test.go` — equivalence classes:
      reordered, aliased, compound-vs-atomic, and explicit-default spellings all
      collapse to one identical canonical string; empty set yields `_`.
- [x] **Task 7 (6e414ed):** `internal/urlsign/verify.go` — `NewVerifier(keyHex, saltHex, size)`
      decoding hex at construction, and `Verify(signature, path)` computing
      HMAC-SHA256 over `salt || path`, truncating to `size`, and comparing with
      `hmac.Equal`. Accepts URL-safe base64 with or without padding.
- [x] **Task 8 (6e414ed):** `internal/urlsign/verify_test.go` — verify against the
      reference vector derived from imgproxy's documented algorithm (not from our
      own output); wrong key, wrong salt, tampered option, tampered source,
      truncated signature; `size` truncation on both sides; malformed hex
      rejected at construction.

---

## Phase 2: Resizer semantics  [x] Complete

**Goal:** `ImageOptions` carries the new fields and `LibvipsResizer.Resize`
grows a second pipeline entered only when `ResizingType != ""`. The legacy
branch is not edited.

**Verification:** `make test` green. The pre-existing `resizer_test.go` and
`server_test.go` assertions pass with **zero edits** — that is the proof that
the legacy path is untouched.

### Tasks

- [x] **Task 9 (65688c9):** `internal/types/types.go` — extend `ImageOptions` with
      `ResizingType`, `Enlarge`, `Extend`, `ExtendAspectRatio`, `Gravity`,
      `Crop`, `Background`. All zero-valued by default so every existing
      construction site compiles and behaves identically.
- [x] **Task 10 (316e42f):** `internal/resizer/resizer.go` — add the branch at the top of
      `Resize`: `ResizingType != ""` enters the new pipeline, otherwise fall
      through to today's body verbatim. Both branches share the existing export
      switch.
- [x] **Task 11 (316e42f):** Crop stage — applied **before** resize per spec F5. Relative
      (`0 < v < 1`) versus absolute dimensions; gravity to offsets via
      `ExtractArea`; `sm` via `SmartCrop`; `fp:x:y` focus point.
- [x] **Task 12 (316e42f):** Resize stage — map `fit` / `fill` / `fill-down` / `force` /
      `auto` onto `vips.Interesting` plus the matching `vips.Size*`, with
      `enlarge` gating upscale. `auto` branches on source-vs-target orientation
      and delegates to `fill` or `fit`.
- [x] **Task 13 (316e42f):** Extend stage — `extend` pads to the requested size and
      `extend_aspect_ratio` pads to the requested ratio, both via `Embed` with
      gravity-derived offsets and the configured background colour.
- [x] **Task 14 (316e42f):** Background stage — hex and `R:G:B` forms to `vips.Color`;
      flatten alpha for formats that cannot carry it; no-args `bg:` disables.
- [x] **Task 15 (316e42f):** `internal/resizer/resizer_test.go` additions — per-resizing-type
      output dimensions for a landscape source, a portrait source, and a source
      smaller than the request; crop-before-resize ordering; `enlarge` on and
      off; `extend` and `extend_aspect_ratio` padding geometry; colour
      equivalence across `fff`, `ffffff`, and `255:255:255`.
- [x] **Task 16 (316e42f):** Verify — run the full existing resizer and server suites with
      no assertion edits, confirming the legacy branch is byte-identical.

---

### Phase 2 findings

- **libvips `SizeDown` short-circuits.** When the source is already smaller
  than the requested box, `ThumbnailWithSize` returns it untouched and skips
  the crop entirely, so the result never reaches the requested aspect ratio.
  The naive mapping of `fill-down` onto `ThumbnailWithSize(w, h, Centre,
  SizeDown)` was therefore wrong, and silently so — it returned a plausible
  image at the wrong ratio. `fillTo` now separates the scale from the crop.
  This is the semantic-drift risk the spec scored at 8; it was real, and the
  dimension assertions caught it.
- **`fill` with `enlarge:0` is equivalent to `fill-down`.** Both never
  enlarge and both land on the requested ratio. This matches imgproxy, where
  `fill-down` exists because plain `fill` would otherwise enlarge.
- **`force` ignores `enlarge`** — it stretches to the exact box by
  definition. Documented in the code and asserted in the tests.

## Phase 3: Route wiring, configuration, documentation  [x] Implementation complete

**Goal:** `/_p/` works end to end behind env gating, and the docs describe it.

**Verification:** `make test` and `make test-debian` both green; the new
`server_test.go` cases cover the full 404 / 403 / 400 / 200 matrix through
`httptest` with the existing mock idiom.

### Tasks

- [x] **Task 17 (d51b573):** `internal/server/server.go` — extract the origin candidate-key
      ladder out of `handleResize` into
      `originCandidateKeys(clientId, folder, path, format) []string`. Pure
      refactor; `handleResize` calls it; the existing tests must pass untouched.
- [x] **Task 18 (d51b573):** `internal/server/server.go` — `urlSigner` field and
      `SetURLSigner(*urlsign.Verifier)`, mirroring the `SetWorkerAuthToken`
      shape and doc-comment convention.
- [x] **Task 19 (d51b573):** `internal/server/server.go` — add the `_p/` rung at the top of
      the dispatch ladder in `ServeHTTP`, gated on the signer being configured;
      a nil signer means the prefix falls through to the ordinary 404 (spec F7).
- [x] **Task 20 (04bec82):** `internal/server/server.go` — `handleParams`: verify signature
      → parse options → enforce `MAX_DIMENSION` **before** any S3 or libvips call
      → build the canonical key → cache-hit `Get` via `effectiveReadClient` →
      resolve the origin through `originCandidateKeys` → short-circuit `raw` and
      matched `skip_processing` with no `Put` → `Resize` → `putBoth` → serve.
      Every S3 and resize call site wrapped in `s.time` with the existing phase
      names so the access-log contract (I12, I13) holds.
- [x] **Task 21 (04bec82):** `internal/server/server.go` — `paramCacheKey` producing
      `{clientId}[-{group}]/_p/{canonical}/{rest-of-tail}` per spec F9.
- [x] **Task 22 (f5e2bfb):** `cmd/image-proxy/main.go` — read `SIGNATURE_KEY`,
      `SIGNATURE_SALT`, `SIGNATURE_SIZE`, `ALLOW_UNSAFE_URLS`, `MAX_DIMENSION`;
      `log.Fatal` naming both vars when exactly one of the key/salt pair is set;
      loud warning when unsafe URLs are enabled; construct the verifier and call
      `SetURLSigner`.
- [x] **Task 23 (04bec82):** `internal/server/server_test.go` — route disabled returns 404
      with no new log noise; the 403 matrix (wrong key, tampered option,
      tampered source, `unsafe` without the flag); the 400 matrix including the
      dimension cap asserting the mock S3 client is **never** called; cache hit
      and miss; `putBoth` targets per `CacheMode`; `X-Use-Cache` override;
      `raw` and skipped responses issuing zero `Put` calls; a `files/`-form tail.
- [x] **Task 24 (d7024b4):** Update documentation (`README.md`) — new "Processing options"
      section covering the URL grammar, the full option table, a worked signing
      recipe with a concrete key/salt/URL example, the five new env vars, and the
      cache-key shape. Run `/draft:documentation readme` if a fuller pass is wanted.
- [x] **Task 25 (a46bbf0):** `draft/architecture.md` — add invariants for fail-closed
      enablement, the signed-payload shape, the canonical cache key, and
      raw/skip-never-writes; extend the §7.2 config table with the five new vars
      and the §3.2 URL-family table with the `_p` family.
- [x] **Task 26 (a46bbf0):** Verify — `make fmt`, then `make test` and `make test-debian`
      both green (libvips behaviour differs between Alpine and Debian, and this
      track adds crop, embed, and smart-crop call sites).
- [ ] **Task 27 (deploy-gated — open by design):** Run `/draft:deploy-checklist` before deploying, and stage the
      rollout per spec §Deployment Strategy (staging unsafe → staging signed →
      production signed).

---

## Notes

- **Ship dark.** Nothing in Phases 1–2 is reachable, and Phase 3 is 404 until
  `SIGNATURE_KEY` / `SIGNATURE_SALT` are set. The branch can merge before the
  webshop's signer exists.
- **The signed payload is the compatibility contract.** It is `/{options}/{source}`
  — excluding `/_p/{signature}` — precisely so an off-the-shelf imgproxy signing
  SDK works unmodified. Task 8's reference vector must come from the documented
  algorithm, not from our implementation, or the test proves nothing.
- **Guardrails in force:** no `os.Getenv` outside `main.go` (Task 22 is the only
  env reader); no router framework (Task 19 is one more rung on the ladder); no
  mocking library (Tasks 15 and 23 use the struct-of-function-pointers idiom);
  no new third-party dependency.
- **Catalog-team coordination is not required for this track** — the worker
  trigger envelope is unchanged (spec Non-Goals). It *will* be required for the
  follow-up that teaches the pre-warm path about canonical option sets.


## Status

Tasks 1-26 are done and verified. **Task 27 is deliberately still open**: it
is the pre-deploy gate, not an implementation step, and running it now would
be meaningless. Trigger `/draft:deploy-checklist` when the deploy is actually
scheduled, then stage the rollout per spec §Deployment Strategy — staging
unsafe, staging signed, production signed.

Verification evidence for tasks 1-26: `make test` (Alpine) and
`make test-debian` both green across all seven packages, `gofmt -l` clean,
no `os.Getenv` outside `main.go`, and zero third-party imports in the two
new packages.

## Tech Debt

| ID | Location | Description | Severity | Payback Trigger |
|----|----------|-------------|----------|-----------------|
| TD-1 | `internal/server/params.go:handleParams` | The signed payload is built from the decoded `r.URL.Path`, not `EscapedPath()`. Source keys containing characters that require percent-encoding would sign differently than a client expects. The legacy regex alphabet (`[\w.\-]`) cannot produce such a key, so this is unreachable today. | Low | If source keys ever admit spaces, `+`, `%` or non-ASCII |
| TD-2 | `internal/urlsign/verify.go:NewVerifier` | The `len(key) == 0` guard is unreachable — any non-empty valid hex decodes to at least one byte, and empty input is caught earlier. Kept as defence. | Low | Never, unless the key source changes |
| TD-3 | `draft/architecture.md` §9 gap #5 | Pre-existing and unrelated to this track: the doc still lists worker-trigger auth as an open gap, but PR #5 closed it. The doc was generated at `6f2e71a`, one commit before. | Low | Next `/draft:init` refresh |
