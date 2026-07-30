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

# Specification: Support SVG originals with transparency preserved

| Field | Value |
|-------|-------|
| **Branch** | `support-svg-transparency` → `none` |
| **Commit** | `8258427` — feat: optional bearer-token auth on POST /_/worker/trigger (#5) |
| **Generated** | 2026-07-28 |
| **Synced To** | `8258427a9bf7912be133749d1286c53bea4723b4` |

**Track ID:** support-svg-transparency
**Type:** feature
**Status:** [x] Complete

## Context References

- **Product:** `draft/product.md` — "P0: Format conversion png/jpg/webp/avif". SVG becomes a supported
  *input* (original). URL contract is frozen: the alpha toggle is an **optional, additive** path segment so
  existing frontend URLs keep working untouched.
- **Tech Stack:** `draft/tech-stack.md` — libvips via `govips/v2`; alpha-capable outputs are png/webp/avif.
  Builds must work in **both** Alpine and Debian images (libvips packaging differs — the load-path risk).
- **Architecture:** `draft/.ai-context.md` — routing/normalized-key in `internal/server`, rasterization in
  `internal/resizer`, bulk pre-resize in `internal/worker`.

## Problem Statement

The proxy cannot rasterize SVG originals, and transparency is unconditionally destroyed:

1. **No SVG input handling.** The resizer relies on libvips to sniff the input format on load
   (`resizer.go:36-52`). Whether SVG loads at all depends on libvips being built with **librsvg** — and
   neither `Dockerfile.alpine` nor `Dockerfile.debian` installs it explicitly. This is unverified today.
2. **Transparency is always destroyed.** `types.ImageOptions.KeepAlpha` exists but is **never set**, so the
   flatten branch at `resizer.go:112` always fires — every transparent source is flattened to white. An SVG
   logo with a transparent background comes out on a white box.
3. **No way to control alpha per request** for cases where the same source is wanted both transparent and
   flattened-on-white on different pages.

## Background & Why Now

Catalog `branding` (and some `blocks`) assets are authored as SVG. Requests for them as raster variants
either 404 or land on a white background — visually broken against non-white page chrome. Some surfaces
legitimately want the same asset flattened (e.g. a card thumbnail on white). The feature therefore needs a
sensible default **plus** an opt-in override.

## Requirements

### Functional

- **FR1 — SVG rasterization.** When a resolved original is an SVG, the resizer loads and renders it to the
  requested raster output format (`png` / `jpg` / `jpeg` / `webp` / `avif`) at the requested width/height,
  through the same fit/thumbnail pipeline as raster originals.

- **FR2 — Auto default by input type.** With no override present:
  - **SVG original** → keep alpha when the output format supports it (png/webp/avif); jpg flattens.
  - **Raster original** (png/jpg/…) → flatten to white when it has alpha, exactly as today (no regression).

  The resizer determines input type itself via `vips.DetermineImageType(data)`; callers do not classify.

- **FR3 — Optional path-segment override.** An optional URL segment overrides FR2 for **any** source:
  - `.../<height>/flat/<file>` (resize path) or `.../<folder>/flat/<file>` (folder path) → **force flatten
    to white**.
  - `.../<height>/alpha/<file>` / `.../<folder>/alpha/<file>` → **force keep alpha** (subject to format —
    see FR6).
  - Segment **absent** → FR2 default.

  `flat` and `alpha` are reserved keywords. Because the `path` group never contained a `/`, this segment is
  purely additive and cannot shadow any existing filename or folder — existing URLs are unaffected.

- **FR4 — Override is part of the cache key.** The override segment is included in the normalized key built by
  `getNormalizedKey` (`server.go:607`), so `logo.png`, `flat/logo.png`, and `alpha/logo.png` cache as three
  distinct variants and never collide. The override is **not** part of the original-source key
  (`originalKey` in `handleResize`) — it's a render directive, not a source path component.

- **FR5 — Bulk pre-resize handles SVG originals.** `POST /_/worker/trigger` batches whose `images` include
  SVG originals rasterize correctly to each requested output format × size using the **FR2 auto default**
  (SVG → alpha for alpha-capable formats). No per-batch alpha override is added to the trigger payload
  (see Non-Goals). Expected worker code change: none beyond what FR2 requires (the resizer decides).

- **FR6 — jpg cannot carry alpha.** `alpha` forced on a jpg/jpeg output still flattens, because the format
  has no alpha channel. This is correct behavior, not an error — documented, not guarded against.

### Non-Functional

- **NFR1 — Dual-image parity.** SVG loading and all alpha behavior identical under Alpine and Debian
  (`make test` and `make test-debian`). If librsvg must be added to one image, add it to both.
- **NFR2 — No new failure cascade.** An SVG that fails to load/render fails only that request/output
  (existing per-output `log + skip` model), never a batch or an unrelated request.
- **NFR3 — Preserve idioms & contract.** Keep the `types.Resizer` interface shape usable; struct-of-func
  mocks + table-driven tests; no new env-var globals outside `main.go`; no `vips.Startup` outside
  `LibvipsResizer.Startup`. Regex edits must not change matching for any existing (segment-absent) URL.

## Acceptance Criteria

- [ ] SVG fixture (`tests/fixtures/logo.svg`, transparent) → `png` retains alpha; → `webp`/`avif` retains
      alpha; → `jpg` flattens to white with no error (assert on the alpha channel, not bytes).
- [ ] SVG renders at requested dimensions (a `240x0` request → 240-wide raster, aspect preserved).
- [ ] **FR2 no-regression:** raster `tests/fixtures/transparent.png` → `png` with **no** override still
      flattens to white (current behavior preserved).
- [ ] **FR3 override, raster:** `alpha/` on `transparent.png` → `png` keeps its transparency.
- [ ] **FR3 override, SVG:** `flat/` on `logo.svg` → `png` is flattened to white.
- [ ] **FR4 cache key:** `getNormalizedKey` produces distinct keys for absent / `flat/` / `alpha/` on the
      same asset; the computed `originalKey` for the source is identical across all three.
- [ ] **Regex safety:** every existing (segment-absent) URL in `server_test.go` still matches the same
      regex and normalizes to the same key as before this change.
- [ ] **FR5:** a `worker.ProcessBatch` over an SVG original produces alpha-preserving png/webp/avif and a
      flattened jpg (mock-client byte inspection).
- [ ] Both `make test` (Alpine) and `make test-debian` pass, proving librsvg present in both images.
- [ ] `Dockerfile.alpine` and `Dockerfile.debian` explicitly declare the SVG loader dependency.

## Non-Goals

- **No SVG output / passthrough via the resize path.** `svg` is an **input** format only, never emitted and
  never added to the output `allowedFormats` map. Stored `.svg` objects are already served verbatim by the
  existing direct-serve path (`ServeHTTP` step 1), independent of this track.
- **No query-param toggle.** Query strings are absent from the cache key, so a param would be a no-op after
  first render. The override is a path segment specifically so it participates in caching.
- **No per-batch alpha override in `POST /_/worker/trigger`.** The batch uses the FR2 auto default only.
  Adding an alpha field to the payload would need catalog-team coordination and is deferred.
- **No new render controls** beyond the alpha toggle (no cropping, filters, background-color choice, etc.).
- **No SVG sanitization** beyond what librsvg does when rasterizing (we never re-serve transformed SVG bytes).
- **No animated-SVG / SMIL** handling — SVG treated as a single static page.

## Technical Approach

**Load path (`internal/resizer/resizer.go`).** libvips sniffs input on `LoadImageFromBuffer` — no explicit
SVG branch needed to *load*, provided librsvg is compiled in. Existing `NumPages.Set(-1)` on the animated
path is harmless for single-page SVG.

**Alpha engine (tri-state).** Model the decision as three states rather than the dead `KeepAlpha` bool:
- Introduce an alpha mode on `ImageOptions` — e.g. `AlphaMode` with values `auto` (default/zero), `keep`,
  `flatten`. (`KeepAlpha` is retired or folded into this; it is currently dead so no caller breaks.)
- In `Resize`, compute the effective decision:
  - `auto`  → `keep = inputIsSVG && formatSupportsAlpha(opts.Format)` where
    `inputIsSVG = vips.DetermineImageType(data) == vips.ImageTypeSVG`.
  - `keep`   → `keep = formatSupportsAlpha(opts.Format)` (jpg still can't, FR6).
  - `flatten`→ `keep = false`.
  - Flatten to white iff `!keep && image.HasAlpha()` (generalizes the existing `resizer.go:112` guard).
- `formatSupportsAlpha(png|webp|avif) = true` — single shared helper, one source of truth.

**URL parsing + cache key (`internal/server`).**
- Extend Regex 1 and Regex 3 with an **optional** keyword segment before `<path>`:
  `…/(?P<height>…)/((?P<alpha>flat|alpha)/)?(?P<path>[\w\.\-]+)$` (and the analogous spot after `<folder>`
  in Regex 3). The trailing `/` on the keyword makes `flat.png` (a real filename) fall through to `<path>`
  unambiguously.
- Map the captured keyword → `AlphaMode` and set it on `opts` in `handleResize`.
- Include the keyword in `getNormalizedKey` (same position) so the cache key differs per mode (FR4).
- Exclude it from `originalKey` — the source lookup is mode-independent.

**Worker.** No parsing change; `ProcessBatch` hands original bytes to the resizer, which applies FR2 auto.
`svg` is not added to `allowedFormats`.

**Docker.** Verify then explicitly install the SVG loader: Alpine `librsvg` (final stage); Debian
`librsvg2-2` / `librsvg2-common` (final stage). Prove with a fixture round-trip in CI, not package presence.

**Fixtures.** `tests/fixtures/logo.svg` staged (Byflou wordmark: `viewBox="0 0 596 43"`, fill `#1d1d1b`, no
background/opacity → transparent; no explicit width/height → renders at 596×43 default DPI then thumbnails —
also a good wide-aspect scaling case). Optionally add a solid-fill SVG for the opaque path.

## Success Metrics

| Category | Metric | Target | Measurement |
|----------|--------|--------|-------------|
| Quality | SVG→png/webp/avif alpha preserved (auto) | 100% of AC cases | resizer + worker tests |
| Quality | Override honored both directions | flat & alpha AC cases pass | server tests |
| Quality | Dual-image parity | Alpine + Debian both green | `make test` / `make test-debian` |
| Correctness | No regression on segment-absent URLs | all existing tests pass | CircleCI `test` job |

## Risk Assessment

| Risk | Probability | Impact | Score | Mitigation |
|------|-------------|--------|-------|------------|
| R1 — libvips in Alpine/Debian image lacks librsvg → SVG won't load at all | 3 | 5 | 15 | Prove with fixture round-trip first (Phase 1); explicitly add librsvg to both Dockerfiles; gate on both test jobs |
| R2 — Regex edits change matching/normalization for existing segment-absent URLs | 2 | 5 | 10 | Optional keyword group + trailing-slash disambiguation; dedicated test asserting every existing URL matches + normalizes unchanged; keyword can't shadow `/`-free paths |
| R3 — Alpha behavior differs between Alpine/Debian libvips builds | 2 | 3 | 6 | Same fixtures asserted in both jobs; assert on alpha channel, not byte-equality |
| R4 — Malicious/oversized SVG (billion-laughs, huge canvas) resource blowup at rasterize | 2 | 4 | 8 | librsvg built-in limits; existing per-request failure isolation; `MaxCacheMem` bound; deep hardening out of scope |
| R5 — Cache fill: `alpha/`/`flat/` variants multiply stored objects for the same asset | 2 | 2 | 4 | Variants are opt-in (frontend only emits when needed); documented; lifecycle unchanged |

## Deployment Strategy

- **Rollout:** Standard single-binary deploy via CircleCI → Docker Hub. No feature flag; the change is
  additive (SVG support + optional segment) and the default path preserves existing raster behavior (FR2).
- **Rollback:** Revert the image; no data migration. Cached variants written during the window stay valid.
- **Frontend:** Existing URLs need no change. Emitting `flat/` or `alpha/` is a separate, scoped frontend
  opt-in — coordinate if/when a surface wants the non-default.
- **Monitoring:** stdout logs; watch resize-error rate for SVG keys and any 404 rate change on `branding`.

## Open Questions

- None blocking. Exact spelling of the keywords (`flat`/`alpha`) and the `AlphaMode` representation are
  implementation details finalized in the plan.

## Conversation Log

- **SVG behavior → Rasterize only.** Input-only; render to the requested raster format at size. No svg-out;
  stored `.svg` objects already serve verbatim via direct-serve.
- **Transparency → auto default by input type, overridable.** Initial "auto by output format" was narrowed:
  the user flagged that existing raster PNGs should keep flattening to white by default. Landed on
  **input-type auto** (SVG keep / raster flatten) so there's no regression on existing PNGs.
- **Override mechanism → optional path segment (`flat`/`alpha`), not a query param.** Decisive factor: the
  cache key is derived from the URL **path** (`getNormalizedKey`), so a query param would be a no-op after
  the first render, while a path segment participates in caching. Segment is optional/additive → existing
  URLs (and the frozen URL contract) are unaffected; only opt-in URLs change.
- **Worker scope → SVG originals via auto default; no payload alpha field** (avoids catalog-team coordination).
- **Primary risks:** R1 librsvg not explicitly installed in either Dockerfile (unverified load support);
  R2 regex edits must not disturb existing segment-absent matching.
- **Fixture:** user supplied `logo_2.svg` (Byflou wordmark) → staged as `tests/fixtures/logo.svg`;
  transparent, wide aspect, viewBox-only (default-DPI render path). Host has no libvips, so R1 is only
  provable in-container (Phase 1).
