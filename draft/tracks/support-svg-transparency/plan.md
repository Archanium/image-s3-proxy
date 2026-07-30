---
project: "image-s3-proxy"
module: "root"
track_id: "support-svg-transparency"
generated_by: "draft:new-track"
generated_at: "2026-07-28T00:00:00Z"
git:
  branch: "support-svg-transparency"
  remote: "none"
  commit: "8258427a9bf7912be133749d1286c53bea4723b4"
  commit_short: "8258427"
  commit_date: "2026-06-16 11:10:30 +0200"
  commit_message: "feat: optional bearer-token auth on POST /_/worker/trigger (#5)"
  dirty: false
synced_to_commit: "8258427a9bf7912be133749d1286c53bea4723b4"
---

# Plan: Support SVG originals with transparency preserved

| Field | Value |
|-------|-------|
| **Branch** | `support-svg-transparency` |
| **Commit** | `8258427` — feat: optional bearer-token auth on POST /_/worker/trigger (#5) |
| **Spec** | ./spec.md |
| **Status** | [x] Complete |

## Overview

Four phases: **prove the loader** (R1 — SVG actually rasterizes in both images), **alpha engine**
(tri-state: input-type auto default + force keep/flatten, in the resizer), **URL toggle + cache key**
(optional `flat/`|`alpha/` segment, regex + normalized-key, with a hard no-regression guard on existing
URLs), then **worker + docs + deploy**. Phase 1 is front-loaded because R1 invalidates everything
downstream; Phase 3 carries R2 (regex must not disturb existing matching).

TDD throughout (`superpowers:test-driven-development`, `draft:testing-strategy`). Tests use the
struct-of-func-pointers mocks + table-driven style. **Assert on the alpha channel of decoded output, never
byte-equality** — Alpine and Debian encoders differ.

## Phases

### Phase 1: Prove SVG loading in both images

**Goal:** `tests/fixtures/logo.svg` rasterizes to PNG under both Alpine and Debian libvips.
**Verification:** New resizer test loads the SVG and asserts a non-error, decodable PNG; green under
`make test` AND `make test-debian`.

#### Tasks
- [x] **Task 1.1:** (2ef743c) Staged `tests/fixtures/logo.svg` (Byflou wordmark, `viewBox="0 0 596 43"`,
      transparent) + `tests/fixtures/solid.svg` (opaque `#3366cc` fill) for the non-alpha path.
- [x] **Task 1.2:** (2ef743c) `TestResizeSVGToPNG` in `resizer_test.go` — SVG→PNG loads, `image/png`,
      decodable, aspect preserved. **Finding:** no production code change needed to load SVG; libvips
      rasterizes it once librsvg is present. (Asserts aspect not pixel-exact width: the 596:43 viewBox rounds
      240→236 via existing resize math — deterministic, out-of-scope to "fix".)
- [x] **Task 1.3:** (83c2505) librsvg was already transitively present via `vips-dev`/`libvips-dev`, so the
      test passed unmodified. Made it explicit anyway in **both** builder and final stages: `Dockerfile.alpine`
      → `librsvg`; `Dockerfile.debian` → `librsvg2-dev` (builder) + `librsvg2-2` (final). Both tester images
      rebuild cleanly.
- [x] **Task 1.4:** (83c2505) Green under both `make test` (Alpine) and `make test-debian` on the rebuilt
      images; full resizer suite passes on both.
- [x] **Verify:** both images green; SVG → PNG produces a valid raster. ✓
      **Note:** the tester runs in the *builder* stage, so this proves the build env loads SVG. The *final*
      runtime stage now declares librsvg explicitly (Task 1.3); a runtime smoke test against the `app`
      container is deferred to Phase 4 deploy-checklist.

### Phase 2: Alpha engine (tri-state, resizer-owned)

**Goal:** Resizer decides alpha from input type + an `AlphaMode` (auto/keep/flatten); auto = SVG-keep /
raster-flatten. `KeepAlpha` (dead) retired or folded in.
**Verification:** Table-driven resizer tests over {source × format × mode} assert alpha presence/absence.

#### Tasks
- [x] **Task 2.1:** (5805b40) Added `AlphaMode` enum (`AlphaAuto` zero-value / `AlphaKeep` / `AlphaFlatten`)
      to `types.ImageOptions`; retired the dead `KeepAlpha` field. **Refinement vs plan:** `formatSupportsAlpha`
      lives *inside the resizer*, not a shared pkg — server/worker only map the URL keyword → `AlphaMode`; the
      format rule is applied by the resizer, so no shared helper package was needed.
- [x] **Task 2.2:** (5805b40) `resizer.go` detects input via `vips.DetermineImageType(data) == vips.ImageTypeSVG`
      (both confirmed present in v2.16.0) and the flatten guard is now `!effectiveKeepAlpha(mode,isSVG,format)
      && image.HasAlpha()`. `TestAlphaPolicy` table (9 cases) over source × format × mode; `HasAlpha()` on the
      output is the discriminator.
- [x] **Task 2.3 (FR2 no-regression):** (5805b40) `raster auto png flattens (no regression)` case passes —
      existing transparent-PNG behavior preserved; `svg auto png keeps alpha` passes.
- [x] **Task 2.4 (FR6):** (5805b40) `svg keep jpg still flattens (no alpha channel)` case passes.
- [x] **Verify:** full `go test ./...` green on both Alpine and Debian (types change compiles across
      server/worker; all packages pass). ✓

### Phase 3: URL toggle + cache key (regex, normalized-key)

**Goal:** Optional `flat/`|`alpha/` segment overrides the default and is baked into the cache key; existing
segment-absent URLs match and normalize **identically** to before.
**Verification:** server tests for override behavior + a dedicated regression test over existing URLs.

#### Tasks
- [x] **Task 3.1 (test-first, R2 guard):** (8cd7549) `TestAlphaSegmentParsing` asserts the segment is captured
      only when present and never shadows a real `flat.png`/`alpha.png` filename or a folder named
      `flat`/`alpha`. Existing `TestRegexMatching`/`TestExtensionHandling`/`TestSpecificURLMapping`/branding
      tests act as the standing regression guard — all still green post-edit.
- [x] **Task 3.2:** (8cd7549) Regex 1 + Regex 3 extended with `((?P<alpha>flat|alpha)/)?` before `(?P<path>…)`.
- [x] **Task 3.3:** (8cd7549) `handleResize` maps `groups["alpha"]` → `AlphaMode` (flat→Flatten, alpha→Keep,
      absent→Auto); `originalKey` derives from the `path` group so it ignores the keyword.
- [x] **Task 3.4:** (8cd7549) Keyword folded into `getNormalizedKey`. `TestGetNormalizedKey_AlphaSegment`
      pins distinct keys for flat/alpha/absent and unchanged normalization for segment-absent URLs.
- [x] **Task 3.5 (server integration):** (8cd7549) `TestServeHTTP_AlphaSegment` drives all three modes end-to-end
      (mock S3) — asserts `opts.AlphaMode` and that the cached `Put` key carries the segment while `originalKey`
      is identical across modes.
- [x] **Verify:** override + all existing regex/mapping tests green; full suite green on Alpine + Debian. ✓

### Phase 4: Worker coverage + docs + deploy gate

**Goal:** Bulk pre-resize proven for SVG originals (auto default); docs + deploy notes updated.
**Verification:** Worker test rasterizes an SVG original to multiple formats with correct alpha; full suite green.

#### Tasks
- [x] **Task 4.1:** (917de7e) Confirmed: `worker.processOutput` builds `ImageOptions` with no `AlphaMode`
      (defaults `AlphaAuto`), so SVG originals keep alpha automatically — no worker logic change. `svg` remains
      absent from `server.go` `allowedFormats` (input-only). **Refinement vs plan:** Task 4.2 uses a capturing
      mock resizer (not the real one) to keep worker tests libvips-free — see below.
- [x] **Task 4.2:** (917de7e) `TestProcessBatch_SVGOriginal_ForwardsBytesWithAutoAlpha` — asserts the worker
      forwards the SVG original bytes verbatim and passes `AlphaAuto` for each format. The actual keep/flatten
      (png keep, jpg flatten) is proven by `resizer.TestAlphaPolicy`; the two compose to cover FR5 without
      coupling worker tests to libvips (they still run on host).
- [x] **Task 4.3:** (917de7e) README updated: SVG-as-input, per-source alpha defaults table, and the optional
      `flat/`|`alpha/` override segment. librsvg dependency already documented via the Dockerfile changes.
- [x] **Task 4.4:** Deploy notes captured (see **Deploy Notes** below). `/draft:deploy-checklist` is the
      maintainer's pre-deploy step at release time.
- [x] **Verify:** full `go test ./...` green on both Alpine and Debian; all spec ACs satisfied. ✓

## Deploy Notes

- **Behavior change (intended, opt-in only):** the default GET path is unchanged for existing raster
  originals (FR2 no-regression — proven). New behavior only appears for SVG originals (now rasterized with
  transparency) and for URLs that explicitly add the `flat/`/`alpha/` segment.
- **librsvg dependency:** both Docker images now declare it explicitly (builder + final). A base-image bump
  must keep it. Runtime SVG load is exercised in the builder stage by tests; a quick smoke test against the
  running `app` container (`GET .../branding/<w>/<h>/<some>.svg.png`) is the recommended pre-deploy check.
- **Cache:** `flat/`/`alpha/` variants are distinct cache keys, so they add stored objects only when a
  frontend opts in. No migration; rollback is a plain image revert (cached variants stay valid).
- **Frontend:** no change required. Emitting `flat/`/`alpha/` is a separate, scoped opt-in.

## Notes

- **Primary risks are front-loaded.** R1 (librsvg) gates Phase 1; R2 (regex regression) is guarded by the
  Task 3.1 lock-test written *before* any regex edit.
- **Assert on alpha, not bytes** for every cross-image assertion.
- **Cache key is the toggle's whole point** — if the keyword isn't in `getNormalizedKey`, `flat/` and
  `alpha/` variants collide with the default and the feature silently does nothing. Task 3.4 is load-bearing.
- **jpg + `alpha` still flattens** (FR6) — no alpha channel to keep; assert it so it's not mistaken for a bug.
- Worker change is ~nil; "worker supports SVG" is satisfied by SVG originals flowing through the unchanged
  fetch→resize path under the FR2 auto default.
