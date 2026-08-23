---
project: "image-s3-proxy"
module: "root"
track_id: "add-imgproxy-url-params"
generated_by: "draft:new-track"
generated_at: "2026-08-22T09:40:25Z"
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

# Specification: imgproxy-style processing options + signed URLs

| Field | Value |
|-------|-------|
| **Branch** | `add-imgproxy-url-params` → (not yet pushed) |
| **Commit** | `8258427` — feat: optional bearer-token auth on POST /_/worker/trigger (#5) |
| **Synced To** | `8258427a9bf7912be133749d1286c53bea4723b4` |

**Track ID:** add-imgproxy-url-params
**Type:** feature
**Status:** [x] Complete

## Context References

- **Product:** `draft/product.md` — the "Compatibility" goal freezes the legacy URL
  contract. This track is strictly **additive**: a second, parallel URL vocabulary.
  It also advances the P1 line on auth surface by introducing HMAC-signed URLs.
- **Tech Stack:** `draft/tech-stack.md` — stdlib-only, no router framework, regex
  ladder dispatch, struct-of-function-pointers mocks, table-driven tests.
- **Architecture:** `draft/architecture.md` §3.2 (URL → key normalization),
  §8.1 (adding a new URL pattern), §2 invariants I2–I5.
- **Guardrails:** `draft/guardrails.md` — no `os.Getenv` outside `main.go`; no
  router framework; no in-process cache; `httpError` for all error responses.

---

## Problem Statement

The proxy today exposes exactly three hard-coded URL shapes, each with resize
behaviour baked into the regex and the handler:

| Family | Shape | Fixed behaviour |
|--------|-------|-----------------|
| `resizeRegex` | `/13/2/images/products/240/336/foo.jpg` | `cover` at v1, `contain` at v2/v3, white padding |
| `fileRegex` | `/13/files/42/doc.pdf` | passthrough |
| `folderImageRegex` | `/13/images/branding/logo.webp` | `inside`, hard-coded 5120×0 |

Every knob that is not width, height, version, or output extension is
unreachable from a URL. There is no way for a client to ask for a different fit
strategy, a non-white pad colour, a crop region, enlargement of a small source,
or an untouched original. Adding each one means another regex family and another
handler branch — the dispatch ladder does not scale.

Separately, **any caller who can reach the listener can mint any URL**. On the
legacy routes that is bounded: the regexes cap width/height at four digits and
constrain the path alphabet, so the reachable variant set is finite and roughly
matches the catalog. A free-form parameter vocabulary removes that bound — an
open parameter endpoint is an unbounded S3-write amplifier and an unbounded
libvips memory amplifier. Parameters and signing therefore ship together, in one
track, and the parameter route is **fail-closed**: inert until keys are configured.

## Background & Why Now

- Four tracks (PRs #1–#5) have hardened the surface around the request path —
  structured access logs, split origin/cache buckets, worker-trigger auth. The
  URL vocabulary itself is the remaining frozen piece.
- imgproxy's processing-option grammar is a well-specified, widely implemented
  vocabulary with existing signing SDKs in most languages. Adopting its syntax
  verbatim (rather than inventing one) means the webshop can use an off-the-shelf
  imgproxy URL builder to generate signed URLs — see the signing decision below,
  which deliberately preserves byte-for-byte signer compatibility.
- The legacy contract cannot be broken (`draft/product.md` → Constraints: "Runs in
  production today; URL contract is frozen for non-breaking work"), so this must
  be a parallel namespace, not a migration.

---

## Requirements

### Functional

#### F1 — URL grammar

```
/_p/{signature}/{option}/{option}/…/{source-tail}
```

- `_p` is a reserved literal prefix. It cannot collide with the legacy families:
  every legacy pattern requires `\d{1,3}` in segment 1.
- `{signature}` is one segment: a URL-safe-base64 HMAC digest, or the literal
  `unsafe`.
- Option segments follow imgproxy syntax `%name:%arg1:%arg2:…`. **Every option
  segment must contain a `:`** — this is what delimits options from the source.
- The **source tail** begins at the first segment that does not match
  `^[a-z_]+:`. Everything from there to the end of the path is the source.

Examples:

```
/_p/oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8/rs:fill:240:336:1/bg:fff/13/products/foo.jpg
/_p/unsafe/w:240/h:336/rt:fill/g:sm/13/products/foo.webp
/_p/{sig}/c:0.5:0.5:ce/w:800/13/branding/logo.png
/_p/{sig}/raw:1/13/products/foo.jpg
```

#### F2 — Source resolution (identical to today)

The source tail resolves to an origin S3 key using **exactly the same candidate
ladder** `handleResize` uses now, for a tail of the form
`{clientId}[-{group}]/{folder}/{file}`:

1. `{clientId}/catalog/{folder}/images/{file}`
2. `{clientId}/catalog/{folder}/images/{file}.{format}`
3. `{clientId}/images/{folder}/{file}`
4. `{clientId}/images/{folder}/{file}.{format}`
5. …then the extension-probe ladder (`jpg jpeg png webp gif avif`, then bare)

A tail whose second segment is `files` (`{clientId}/files/{fileId}/{file}`)
resolves to the literal tail, matching `handleFile`.

Reads go to `originClient` only, so the fallback / lazy-migration behaviour
(invariants I6, I7) applies unchanged and for free.

#### F3 — Output format

The output format is the last `.`-suffix of the final path segment, using the
**existing** rule: a compound extension such as `foo.png.webp` strips the last
component to recover the source name. Same code path, no new vocabulary.

#### F4 — Wave-1 option set

| Long name | Aliases | Arguments | Notes |
|-----------|---------|-----------|-------|
| `resize` | `rs` | `:type:width:height:enlarge:extend` | compound; expands to the atomic five |
| `size` | `s` | `:width:height:enlarge:extend` | compound; expands to the atomic four |
| `width` | `w` | `:int` | `0` = derive from height + source AR |
| `height` | `h` | `:int` | `0` = derive from width + source AR |
| `resizing_type` | `rt` | `fit\|fill\|fill-down\|force\|auto` | default `fit` |
| `enlarge` | `el` | `:bool` | default off |
| `extend` | `ex` | `:bool:gravity` | pad out to the requested size |
| `extend_aspect_ratio` | `extend_ar`, `exar` | `:bool:gravity` | pad out to the requested ratio |
| `gravity` | `g` | `:type[:x:y]` | drives `fill` cropping and both extends |
| `crop` | `c` | `:width:height[:gravity]` | applied **before** resize |
| `background` | `bg` | `:R:G:B` or `:hex` | no args = disable |
| `raw` | — | `:bool` | serve the source bytes untouched |
| `skip_processing` | `skp` | `:ext1:ext2:…` | serve untouched when the source ext matches |

Booleans accept imgproxy's set: `1 t true` / `0 f false`.

Gravity types: `no so ea we noea nowe soea sowe ce`, plus `sm` (smart, crop
only) and `fp:%x:%y` (focus point). Unknown types are a 400.

`crop` width/height are floats: `0` means the full source dimension, a value in
`(0,1)` is a fraction of the source dimension, `>= 1` is absolute pixels.

> `background` appeared twice in the intake description; read as a single option.
> `gravity` was not in the intake list but is added because both `crop` and
> `resizing_type:fill` are meaningless without it.

#### F5 — Resizing-type semantics

| Type | Semantics | libvips realisation |
|------|-----------|---------------------|
| `fit` | scale to fit inside w×h, preserve AR, no crop | `ThumbnailWithSize(..., InterestingNone, SizeBoth)` |
| `fill` | scale to cover w×h, preserve AR, crop the excess at gravity | thumbnail with the gravity-derived `Interesting` |
| `fill-down` | as `fill`, but never enlarge — a smaller source yields a smaller result at the requested AR | thumbnail with `SizeDown` |
| `force` | stretch to exactly w×h, AR ignored | `SizeForce` |
| `auto` | `fill` when source and target share orientation, otherwise `fit` | branch on the two ARs, then delegate |

Operation order: **crop → resize → extend / extend_aspect_ratio → background
flatten → encode.**

#### F6 — Signature verification

- Config: `SIGNATURE_KEY`, `SIGNATURE_SALT` (both hex-encoded, imgproxy
  convention), optional `SIGNATURE_SIZE` (digest truncation in bytes, default
  32), optional `ALLOW_UNSAFE_URLS`.
- **Signed payload:** `salt_bytes || []byte("/" + strings.Join(segments_after_signature, "/"))`
  — i.e. the `/_p/{signature}` prefix is *excluded*. This is exactly what an
  off-the-shelf imgproxy signer produces for `/{options}/{source}`, so existing
  imgproxy signing SDKs work byte-for-byte against this service.
- HMAC-SHA256, truncated to `SIGNATURE_SIZE` bytes, compared with
  `hmac.Equal` (constant time), encoded URL-safe base64 **without padding**.
- Verification failure → **403** with `Cache-Control: max-age=30`.
- `unsafe` is accepted as the signature segment **only** when
  `ALLOW_UNSAFE_URLS=true`; otherwise it is an ordinary signature mismatch → 403.

#### F7 — Fail-closed enablement

When neither `SIGNATURE_KEY`/`SIGNATURE_SALT` nor `ALLOW_UNSAFE_URLS=true` is
configured, the `/_p/` route is **disabled** and returns 404 — indistinguishable
from any other unmatched path. The feature is inert on every existing deployment
until an operator turns it on. Setting only one of `SIGNATURE_KEY` /
`SIGNATURE_SALT` is a **fatal startup error** naming both vars (matching the
existing `CACHE_MODE`/`CACHE_BUCKET` pattern, invariant I15).

`ALLOW_UNSAFE_URLS=true` logs a loud startup warning every time.

#### F8 — Dimension cap

`MAX_DIMENSION` (default `5120`, the value the existing `folderImageRegex` path
already hard-codes) bounds the requested width, height, and both crop
dimensions. A request exceeding it → **400**, before any S3 read or libvips call.

#### F9 — Cache key

Canonical option string, embedded in a `_p` segment under the tenant prefix:

```
{clientId}[-{group}]/_p/{canonical}/{rest-of-source-tail}
```

Canonicalisation, in order:

1. Expand compound options (`rs`, `s`) into their atomic components.
2. Resolve aliases to long names.
3. Drop any option sitting at its documented default.
4. Normalise values — hex colours lowercased and expanded `fff` → `ffffff`;
   booleans to `1`; gravity lowercased; crop floats to their shortest exact form.
5. Sort by long option name, join `name=value` pairs with `,`.
6. An empty option set canonicalises to the literal `_` (never an empty segment).

Worked example — both of these URLs

```
/_p/{sig}/h:336/rs:fill/w:240/bg:ffffff/13/products/foo.jpg
/_p/{sig}/rt:fill/w:240/h:336/bg:fff/13/products/foo.jpg
```

canonicalise to the same key:

```
13/_p/background=ffffff,height=336,resizing_type=fill,width=240/products/foo.jpg
```

`=` and `,` are legal in both S3 keys and URL path segments; no escaping needed.

#### F10 — Cache lifecycle

- Cache-hit lookup on the canonical key runs through `effectiveReadClient`, so
  `CACHE_MODE` and `X-Use-Cache` behave identically to the legacy paths.
- Cache-back write goes through `putBoth` — never a direct `Put`.
- **`raw` and a matched `skip_processing` are served straight from the origin and
  are never cache-written.** Writing them would duplicate originals into the
  cache bucket for zero benefit, and would violate the write-amplification
  guardrail.

#### F11 — Response headers

Unchanged contract: `Cache-Control: max-age=31536000` on 2xx, `max-age=30` on
errors via `httpError`. Phase timings recorded under the existing names
(`s3-get`, `resize`, `s3-put*`) so the access-log and `Server-Timing` shapes
(invariants I12, I13) are untouched.

### Non-Functional

- **N1 — Legacy byte-identity.** The three legacy families must produce
  byte-identical output before and after. The existing `server_test.go` and
  `resizer_test.go` suites pass unmodified.
- **N2 — Additive resizer.** `LibvipsResizer.Resize` branches on
  `opts.ResizingType != ""`. The legacy branch is not edited.
- **N3 — Pure parsing.** Option parsing, canonicalisation, and signature
  verification live in packages with no S3, no HTTP, and no libvips dependency,
  and are exhaustively table-driven-testable without Docker.
- **N4 — Guardrails.** No `os.Getenv` outside `main.go`; no router framework; no
  mocking library; no new third-party dependency (`crypto/hmac`,
  `crypto/sha256`, `encoding/base64`, `encoding/hex` are all stdlib).
- **N5 — Hot-path cost.** Parsing plus HMAC on a cache hit must stay
  well under the S3 GET it precedes; no regex compilation per request.

---

## Acceptance Criteria

**Parsing & canonicalisation**

- [ ] Every wave-1 option parses from both its long name and each alias.
- [ ] `rs` / `s` expand to atomic options identical to spelling them out.
- [ ] Malformed options (bad arity, non-numeric dimension, unknown resizing
      type, unknown gravity, bad hex colour) return 400 naming the failing option.
- [ ] An unknown option name returns 400 naming it.
- [ ] Reordered, aliased, compound-vs-atomic, and default-explicit spellings of
      the same request all canonicalise to one identical string.
- [ ] An empty option set canonicalises to `_`.

**Signature**

- [ ] A URL signed by the reference imgproxy algorithm over `/{options}/{source}`
      verifies successfully.
- [ ] Wrong key, wrong salt, tampered option, tampered source, and truncated
      signature each yield 403.
- [ ] `SIGNATURE_SIZE` truncation is honoured on both sides.
- [ ] `unsafe` succeeds with `ALLOW_UNSAFE_URLS=true` and 403s without it.
- [ ] Comparison uses `hmac.Equal`; no `==` on digest bytes anywhere.

**Routing & enablement**

- [ ] With no signing env configured, `/_p/...` returns 404 and no new log noise.
- [ ] Exactly one of `SIGNATURE_KEY` / `SIGNATURE_SALT` set → `log.Fatal` naming both.
- [ ] `ALLOW_UNSAFE_URLS=true` emits a startup warning.
- [ ] A source tail resolving to a missing original returns 404 after the same
      candidate ladder the legacy path uses.
- [ ] `files/`-form tails resolve to the literal key.

**Behaviour**

- [ ] `fit`, `fill`, `fill-down`, `force`, `auto` each produce the documented
      output dimensions for a landscape source, a portrait source, and a source
      smaller than the request.
- [ ] `crop` is applied before resize; relative (`0.5`) and absolute (`400`)
      forms both work; `crop` + `fill` compose.
- [ ] `enlarge:0` never upscales; `enlarge:1` does.
- [ ] `extend` pads to the requested size at the requested gravity;
      `extend_aspect_ratio` pads to the requested ratio.
- [ ] `background` fills pad area and flattens alpha; `fff` and `255:255:255`
      and `ffffff` are equivalent.
- [ ] `raw:1` returns the source bytes and its `Content-Type` byte-for-byte.
- [ ] `skip_processing:png` on a `.png` source returns the source untouched and
      does not apply to a `.jpg` source.
- [ ] Width, height, or a crop dimension above `MAX_DIMENSION` returns 400
      **before** any S3 read (asserted via a mock that fails the test if called).

**Cache**

- [ ] A cache hit on the canonical key is served without touching the origin.
- [ ] A miss writes back through `putBoth`; `off` / `shadow` / `live` each
      target the documented client(s).
- [ ] `X-Use-Cache: true|false` flips the read source on the new route.
- [ ] `raw` and skipped responses issue **zero** `Put` calls.

**Regression**

- [ ] The full pre-existing test suite passes with no edits to its assertions.
- [ ] `make test` and `make test-debian` both green.

## Non-Goals

- **Not** migrating the legacy URL families onto the new pipeline. The legacy
  resizer branch is not touched, even though `contain` is expressible as
  `fit` + `extend` + `bg:ffffff`.
- **Not** signing the legacy URLs. They stay unsigned; that is the frozen contract.
- **Not** arbitrary source URLs. imgproxy's `plain/`, base64, and `enc/` source
  forms are out — sources remain catalog-relative keys in the configured buckets.
- **Not** the rest of the imgproxy vocabulary: `trim`, `pad`, `dpr`, `zoom`,
  `min-width`/`min-height`, `blur`, `sharpen`, `watermark`, `preset`, `quality`,
  `format`, `auto_rotate`, `strip_metadata`, IPTC/EXIF handling, `/info`.
- **Not** a `format:` option — the extension rule already covers output format.
- **Not** exposing the new vocabulary through `POST /_/worker/trigger`. The
  pre-warm envelope keeps its current shape; wiring it to canonical option sets
  is a follow-up that needs catalog-team coordination.
- **Not** a signature-key rotation mechanism (single key/salt pair only).
- **Not** a router framework. One more rung on the dispatch ladder.

---

## Technical Approach

### New packages

`internal/procopts` — pure. Parses `[]string` segments into an `Options` struct,
validates, and emits the canonical string. Zero imports beyond stdlib. This is
where the whole option vocabulary lives; adding a wave-2 option means one table
entry plus tests.

`internal/urlsign` — pure. `Verifier{key, salt []byte, size int}` with
`Verify(sig, path string) error`. Hex decoding of key/salt happens at
construction so a malformed key is a startup error, not a per-request one.

### Changed packages

`internal/types` — `ImageOptions` gains `ResizingType`, `Enlarge`, `Extend`,
`ExtendAspectRatio`, `Gravity`, `Crop`, `Background`. All zero-valued by
default, so every existing construction site keeps compiling and behaving
identically.

`internal/resizer` — `Resize` gains a leading branch: when
`opts.ResizingType != ""`, run the new crop → resize → extend → flatten →
encode pipeline; otherwise fall through to today's code verbatim. The export
switch at the bottom is shared by both branches.

`internal/server` —
1. Extract the candidate-key ladder out of `handleResize` into
   `originCandidateKeys(clientId, folder, path, format) []string`. Pure
   refactor; `handleResize` calls it and the existing tests must pass untouched.
2. One `strings.HasPrefix(key, "_p/")` check at the top of the dispatch ladder,
   gated on the verifier being configured.
3. `handleParams(w, r, segments)` — verify → parse → cap → canonical key →
   cache-hit `Get` via `effectiveReadClient` → resolve origin → raw/skip
   short-circuit → `Resize` → `putBoth` → serve. Every S3 and resize call site
   wrapped in `s.time(...)` with the existing phase names.
4. `SetURLSigner(*urlsign.Verifier)` on `Server`, mirroring the existing
   `SetWorkerAuthToken` shape.

`cmd/image-proxy/main.go` — reads the five new env vars, validates the pair
rule, constructs the verifier, calls `SetURLSigner`.

### Why a prefix check and not a fourth regex

The option list is variable-length, so a regex would have to be non-greedy over
`(?:[a-z_]+:[^/]*/)*` and would still need a second parsing pass to split the
options. A `HasPrefix` plus `strings.Split` is cheaper, clearer, and keeps the
option grammar in one place (`procopts`) instead of half-encoded in a pattern.

### Alternatives considered

| Alternative | Why not |
|-------------|---------|
| Query-string parameters (`?w=240&h=336`) | Cloudflare's default cache key ignores query strings, and the existing bucket-is-the-cache model keys on path. Would need CDN config changes out of tree. |
| Deploy real imgproxy alongside | Loses the catalog-key resolution, the fallback-bucket lazy migration, the split origin/cache topology, and the access-log contract — all of which are this service's actual value. |
| Extend the legacy regexes with optional groups | Each new option multiplies regex complexity against three patterns that must stay byte-compatible. Rejected on risk. |
| Hash-only cache keys | Loses bucket greppability during incident triage; the option set is small enough that canonical strings stay short. |

---

## Success Metrics

| Category | Metric | Target | Measurement |
|----------|--------|--------|-------------|
| Quality | Legacy suite regressions | 0 | `make test` + `make test-debian` |
| Quality | Coverage of `procopts` + `urlsign` | > 95% | `go test -cover` |
| Performance | `resize` phase p95, new route vs legacy | within 10% | `timings.resize` in the JSON access log |
| Performance | Added non-S3, non-vips overhead per request | < 1 ms | `upstream.responseTime` minus summed `timings` |
| Security | Unsigned `/_p/` requests reaching a resize | 0 | 403/404 counts in the access log |
| Business | Distinct canonical keys per source image | bounded by the client's preset set | cache-bucket prefix listing under `_p/` |

## Risk Assessment

| Risk | P | I | Score | Mitigation |
|------|---|---|-------|------------|
| Unbounded dimensions → libvips OOM / DoS | 3 | 4 | 12 | `MAX_DIMENSION` (default 5120) enforced pre-S3, pre-vips (F8). AC asserts the mock S3 client is never called on a rejected request. |
| Unbounded cache-key cardinality → S3 write amplification | 3 | 4 | 12 | Route is fail-closed (F7); signature required to mint any URL (F6); canonicalisation collapses equivalent spellings (F9); `raw`/skip never write (F10). |
| Legacy regression from shared-code refactor | 2 | 5 | 10 | The only shared edit is extracting `originCandidateKeys`, verified by the existing untouched suite. The resizer's legacy branch is not edited (N2). |
| Signature semantics drift from imgproxy → client SDKs fail | 3 | 3 | 9 | Signed payload is pinned to `/{options}/{source}` and tested against a vector produced by the documented imgproxy algorithm, not against our own implementation. |
| `fill-down` / `auto` semantics diverge from imgproxy | 4 | 2 | 8 | Documented explicitly in F5 with per-orientation dimension assertions; divergence is a known, bounded, documented risk rather than a silent one. |
| `ALLOW_UNSAFE_URLS` left on in production | 2 | 4 | 8 | Loud startup warning on every boot; called out in the deploy checklist. |
| Client adoption diverges from the catalog pre-warm set | 3 | 2 | 6 | Wave 1 is read-path only; the worker envelope is explicitly a non-goal until the option sets settle. |

## Deployment Strategy

**Rollout.** The feature ships dark. No env change → `/_p/` is 404 and the
binary behaves exactly as today (F7). Enablement is per-environment:

1. Staging with `ALLOW_UNSAFE_URLS=true`, no keys — exercise the vocabulary.
2. Staging with keys, unsafe off — validate the webshop's signer end to end.
3. Production with keys, unsafe off. Legacy traffic is unaffected throughout.

**Kill switch.** Unset `SIGNATURE_KEY` / `SIGNATURE_SALT`. The route reverts to
404 on the next boot; already-cached `_p/` objects are simply unreachable and
age out with the bucket's lifecycle policy.

**Rollback.** Revert the deployment. There is no schema and no migration; the
new cache keys live under a `_p/` segment that no legacy path can produce, so
they cannot shadow or corrupt existing cached variants.

**Monitoring.** Access-log queries: 403 rate on `_p/` paths (signer mismatch),
400 rate (client-side vocabulary errors), `timings.resize` p95 split by whether
the path contains `_p/`, and cache-bucket object count under `_p/`.

---

## Open Questions

1. Should the canonical cache key include the output format explicitly, or is the
   file extension on the tail sufficient? Current design: extension is
   sufficient, since it is part of the source tail and therefore part of the key.
2. Do we want `fill-down`'s "never enlarge" to also imply `enlarge:0`, or should
   an explicit `enlarge:1` override it? imgproxy treats them independently;
   this spec follows imgproxy.
3. Long term, should the worker pre-warm accept canonical option strings so the
   catalog can pre-generate parameterised variants? Deliberately out of scope
   here — needs catalog-team coordination.

## Conversation Log

Compact intake; four decisions taken, all as recommended, plus one mid-session
scope addition.

- **URL shape → reserved `/_p/` prefix.** Chosen over imgproxy-exact root
  placement (which would force per-request signature-vs-clientId disambiguation
  against the legacy patterns) and over `clientId`-first placement. `_p` cannot
  match `\d{1,3}`, so the legacy ladder is provably unreachable from the new
  namespace and vice versa.
- **Signing → new route only, env-gated.** Legacy URLs stay unsigned (frozen
  contract). Keys are hex-encoded and the signed payload excludes the
  `/_p/{signature}` prefix specifically so off-the-shelf imgproxy signing SDKs
  work unmodified. Fail-closed enablement was added on top of the chosen option:
  with no keys the route is 404, not "open".
- **Cache key → canonical option string.** Chosen over a short hash and over a
  length-capped hybrid, for bucket greppability during incident triage. The
  option set is small enough that key length is not a practical concern; if
  wave 2 changes that, the hybrid remains available without a URL change.
- **Aspect ratio → `extend_aspect_ratio` only.** Flagged during intake that
  imgproxy has no standalone `aspect_ratio` option; AR *preservation* already
  falls out when width or height is 0. Declined inventing a custom `ar` option
  in order to keep the vocabulary a strict subset of imgproxy's.
- **Cropping added mid-intake.** `crop` / `c` joins wave 1, applied before
  resize per imgproxy ordering. `gravity` / `g` was pulled in alongside it —
  it was not in the original list, but both `crop` and `resizing_type:fill`
  are underspecified without it.
