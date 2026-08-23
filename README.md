# Image Proxy

A Go-based image resizer and proxy with libvips, mirroring the logic of the
original Node.js implementation.

## Features
- Fetches images from S3 (or any S3-compatible storage — Hetzner Object Storage,
  Cloudflare R2, MinIO, etc.).
- Resizes images on-the-fly based on URL patterns.
- Rasterizes **SVG** originals to any raster output format (`png`/`jpg`/`webp`/`avif`),
  preserving transparency by default (see *SVG & transparency* below).
- Caches resized images back to S3.
- Optional split-bucket topology (origin + cache) with a canary migration mode
  for safely moving the cache layer to a different provider.
- Structured JSON access logs to `stdout`, including a per-phase `timings`
  breakdown, plus a matching per-phase `Server-Timing` response header.
- Worker trigger for bulk pre-resize.
- Configurable via environment variables.

## Usage with Makefile

A `Makefile` is provided to simplify common tasks:

- **Build images (Alpine)**: `make build` (or `make build-alpine`)
- **Build images (Debian)**: `make build-debian`
- **Run tests (Alpine)**: `make test` (or `make test-alpine`)
- **Run tests (Debian)**: `make test-debian`
- **Format code**: `make fmt`
- **Start application (Alpine)**: `make up`
- **Stop application**: `make down`

## Running the Proxy

You can use Docker Compose to run the proxy locally:

```bash
make up
```

The server will be available at `http://localhost:8080`.

## Storage backends

The proxy supports two storage topologies:

- **Single-bucket (default).** `CACHE_MODE=off` (or unset). Originals and resized
  variants live in the same bucket — the historical layout.
- **Split-bucket (canary).** `CACHE_MODE=shadow|live`. Originals live in the
  origin bucket (where the upstream catalog system writes); resized variants
  live in the cache bucket. Used to migrate the cache layer to a different
  provider (e.g. Cloudflare R2) without flipping a single switch:

| `CACHE_MODE` | Default read source | Write destinations |
|--------------|---------------------|--------------------|
| `off`        | origin (= the only bucket) | origin |
| `shadow`     | origin              | both — origin first, then cache (populate cache without affecting reads) |
| `live`       | cache               | both — cache first, then origin (cache is primary; origin is belt-and-suspenders) |

Recommended migration sequence:
1. Deploy with `CACHE_MODE=off`. No-op.
2. Provision cache bucket, set `CACHE_MODE=shadow` + `CACHE_BUCKET=<cache>`. Cache populates from real traffic.
3. Test cache read performance with the `X-Use-Cache: true` request header (see below).
4. When cache has enough coverage, set `CACHE_MODE=live`. Default reads flip to the cache bucket.
5. Optionally keep `live` indefinitely for belt-and-suspenders.

### Read-source override (per request)

When `CACHE_MODE` is `shadow` or `live`, the `X-Use-Cache` request header
overrides the default read source for a single request. The header does NOT
affect dual-write — it only controls which client serves the cache-hit `GET`.

- `X-Use-Cache: true` — read from the cache bucket.
- `X-Use-Cache: false` — read from the origin bucket.
- Other values, or header absent — use the mode's default.

This is intended for synthetic monitors that want to benchmark cache reads
while real traffic stays on the default path.

### Environment Variables

Required:
- `BUCKET` — the origin (and, in off mode, only) S3 bucket name.

Origin bucket (always read; same env vars as before):
- `AWS_REGION` — defaults to `us-east-1`.
- `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` — static credentials (if unset, the AWS default credential chain is used).
- `S3_ENDPOINT` — custom endpoint for the origin client (Hetzner / MinIO / etc.).

Cache bucket (only used when `CACHE_MODE != off`):
- `CACHE_MODE` — one of `off | shadow | live`. Default `off`.
- `CACHE_BUCKET` — required when `CACHE_MODE != off`. Startup is fatal otherwise.
- `CACHE_S3_ENDPOINT` — custom endpoint for the cache client (e.g. `https://<account>.r2.cloudflarestorage.com`).
- `CACHE_AWS_ACCESS_KEY_ID` / `CACHE_AWS_SECRET_ACCESS_KEY` — cache-bucket credentials.
- `CACHE_AWS_REGION` — inherits `AWS_REGION` when unset.

Legacy fallback (origin-side migration; unchanged):
- `OLD_S3_BUCKET`, `OLD_S3_REGION`, `OLD_S3_ACCESS_KEY_ID`, `OLD_S3_SECRET_ACCESS_KEY`, `OLD_S3_ENDPOINT` — when set, the origin client consults this bucket as a fallback for not-found lookups and copies hits back to the primary origin bucket. The cache client never has a fallback.

Server:
- `PORT` — server port. Defaults to `8080`.

Auth:
- `WORKER_AUTH_TOKEN` — optional bearer token for `POST /_/worker/trigger`.
  When **unset or empty**, the trigger endpoint is unauthenticated (today's
  default). When **set**, every trigger request must carry
  `Authorization: Bearer <token>`; missing or wrong-token requests get
  `401 Unauthorized` + `WWW-Authenticate: Bearer realm="worker-trigger"`.
  The token is compared in constant time via `crypto/subtle`.
  GET read paths (the three URL regex families) are **not** affected —
  they remain public and rely on Cloudflare / load-balancer controls.

Processing options + URL signing (the `/_p/` route — see below):
- `SIGNATURE_KEY` / `SIGNATURE_SALT` — **hex-encoded** HMAC key and salt,
  matching imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT` convention. Both must
  be set together; setting only one is a fatal startup error. While both are
  unset the `/_p/` route does not exist and returns `404`.
- `SIGNATURE_SIZE` — digest truncation in bytes, 1–32. Defaults to 32 (the
  full SHA-256 digest).
- `ALLOW_UNSAFE_URLS=true` — accept the literal signature `unsafe`, which
  bypasses verification. **Local development only**; logs a warning on every
  boot. Setting this alone (with no key/salt) enables the route in
  unsigned-only mode.
  `docker-compose.yml` sets this for the local `app` services so `make up` gives
  you a working `/_p/` route without a signer. It must never be set in k3s.
- `MAX_DIMENSION` — ceiling for requested width, height and absolute crop
  dimensions. Defaults to `5120`. Enforced before any S3 read or libvips
  call.

Worker (bulk pre-resize defaults):
- `SIZES` — JSON array of target sizes (e.g. `[[150,210],[240,0]]`). Defaults to a predefined list of 33. Used as the fallback when a trigger payload omits `sizes`.
- `FORMAT` — historically the env-wide target format. **No longer used by the trigger** — the new payload requires `formats` explicitly. Kept as a field on the worker struct in case future internal callers need a default.

libvips tuning:
- `VIPS_CONCURRENCY`, `VIPS_MAX_CACHE_MEM`, `VIPS_MAX_CACHE_SIZE`.

Debug:
- `DEBUG=true` — enables libvips logging.

## Processing options (`/_p/`)

> Building URLs from a client? See **[docs/signed-urls.md](docs/signed-urls.md)** —
> the full option reference, signing recipes in four languages, and test vectors.
> The section below is the operator-facing summary.

An imgproxy-compatible URL vocabulary, served alongside — never instead of —
the three legacy URL families, which are unchanged.

```
/_p/{signature}/{option}/{option}/.../{source-tail}
```

- `_p` is a reserved prefix. It cannot collide with the legacy families,
  every one of which requires a numeric client id in the first segment.
- Options use imgproxy syntax, `name:arg:arg`. **Every option segment
  contains a colon** — that is what separates the options from the source.
- The **source tail** starts at the first segment without that shape and runs
  to the end.

The route is **disabled by default**. Until `SIGNATURE_KEY` and
`SIGNATURE_SALT` (or `ALLOW_UNSAFE_URLS=true`) are configured, `/_p/` paths
return `404` like any other unmatched path.

### Source tails

Two shapes are recognised, resolving exactly the way the legacy routes do —
including the multi-layout candidate ladder and the `OLD_S3_BUCKET` fallback
with its lazy copy-back:

| Tail | Resolves to |
|------|-------------|
| `{clientId}[-{group}]/{folder}/{file}` | `{clientId}/catalog/{folder}/images/{file}`, then several fallback layouts |
| `{clientId}/files/{fileId}/{file}` | that literal key |

The output format is the last `.`-suffix of the filename, same rule as the
legacy routes: `foo.png.webp` reads `foo.png` and encodes WebP.

### Options

| Long name | Aliases | Arguments |
|-----------|---------|-----------|
| `resize` | `rs` | `:type:width:height:enlarge:extend` |
| `size` | `s` | `:width:height:enlarge:extend` |
| `width` | `w` | `:int` — `0` derives from height and the source ratio |
| `height` | `h` | `:int` — `0` derives from width and the source ratio |
| `resizing_type` | `rt` | `fit` \| `fill` \| `fill-down` \| `force` \| `auto` (default `fit`) |
| `enlarge` | `el` | `:bool` (default off) |
| `extend` | `ex` | `:bool:gravity` — pad out to the requested size |
| `extend_aspect_ratio` | `extend_ar`, `exar` | `:bool:gravity` — pad out to the requested ratio |
| `gravity` | `g` | `:type[:x:y]` |
| `crop` | `c` | `:width:height[:gravity]` — applied **before** resize |
| `background` | `bg` | `:R:G:B` or `:hex` (3 or 6 digits); no arguments disables |
| `raw` | — | `:bool` — serve the source untouched |
| `skip_processing` | `skp` | `:ext1:ext2:...` — serve untouched when the source extension matches |

Booleans accept `1`/`t`/`true` and `0`/`f`/`false`.

Gravity types: `no`, `so`, `ea`, `we`, `noea`, `nowe`, `soea`, `sowe`, `ce`,
plus `sm` (smart — crop only) and `fp:%x:%y` (focus point, fractional
coordinates). Offsets move the box inward from the edge the gravity names.

Crop dimensions are floats: `0` means the full source dimension, a value
below `1` is a fraction of it, anything else is absolute pixels.

### Resizing types

| Type | Behaviour |
|------|-----------|
| `fit` | Scale to sit inside the box, ratio preserved, nothing cropped |
| `fill` | Scale to cover the box, ratio preserved, crop the excess at the gravity |
| `fill-down` | As `fill`, but never enlarges — a smaller source yields a smaller result at the requested ratio |
| `force` | Stretch to exactly the box, ratio ignored (this one ignores `enlarge`) |
| `auto` | `fill` when source and box share an orientation, `fit` otherwise |

`fill` with `enlarge:0` behaves identically to `fill-down`.

Operation order is **crop → resize → extend → background flatten → encode**.

### Transparency

`/_p/` follows imgproxy's rule — transparency survives into `png` / `webp` /
`avif`, and `bg:` is what flattens it. This differs from the legacy routes,
whose `AlphaAuto` default flattens transparent **raster** originals to white
(see [SVG & transparency](#svg--transparency)). A transparent PNG served through
`/13/1/images/products/240/336/foo.png` comes back flattened; the same source
through `/_p/{sig}/w:240/h:336/13/products/foo.png` keeps its alpha. Add `bg:fff`
to get the legacy look. The `flat/` / `alpha/` segments have no `/_p/`
equivalent and do not need one.

### Passthrough

The source bytes are served untouched, and **nothing is cached**, when any of:

- `raw:1`
- `skip_processing` names the source's extension
- the extension is not an encodable output format (`png`, `jpg`, `jpeg`,
  `webp`, `avif`) — this is what keeps a `files/` PDF from being re-encoded

### Signing

HMAC-SHA256 over `salt ‖ path`, truncated to `SIGNATURE_SIZE` bytes, encoded
as unpadded URL-safe base64.

**The signed path excludes the `/_p/{signature}` prefix.** What gets signed
is exactly `/{options}/{source}` — byte-for-byte what an off-the-shelf
imgproxy signing SDK produces, so an existing imgproxy URL builder works
against this service unmodified.

Worked example, with `SIGNATURE_KEY` and `SIGNATURE_SALT` set to the hex of
`my-secret-key` and `my-salt`:

```
SIGNATURE_KEY=6d792d7365637265742d6b6579
SIGNATURE_SALT=6d792d73616c74
```

Signing `/rs:fill:240:336:1/bg:fff/13/products/foo.jpg` gives:

```
/_p/EfJN7y4nlsfYrvbbyeE0E8fgeWmHpAgCSldArUNfrJA/rs:fill:240:336:1/bg:fff/13/products/foo.jpg
```

Reference implementation:

```python
import hmac, hashlib, base64, binascii

def sign(key_hex, salt_hex, path, size=32):
    key, salt = binascii.unhexlify(key_hex), binascii.unhexlify(salt_hex)
    digest = hmac.new(key, salt + path.encode(), hashlib.sha256).digest()[:size]
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode()

path = "/rs:fill:240:336:1/bg:fff/13/products/foo.jpg"
url = "/_p/" + sign("6d792d7365637265742d6b6579", "6d792d73616c74", path) + path
```

A bad signature returns `403` with `Cache-Control: max-age=30`. During local
development, `ALLOW_UNSAFE_URLS=true` lets you write `unsafe` in place of the
signature.

### Caching

Processed variants are cached under:

```
{clientId}[-{group}]/_p/{canonical}/{rest-of-source-tail}
```

`{canonical}` is a normalised rendering of the option set: aliases resolved
to long names, defaults dropped, values normalised, sorted by name, joined
with `,`. Both of these URLs

```
/_p/{sig}/h:336/rs:fill/w:240/bg:ffffff/13/products/foo.jpg
/_p/{sig}/rt:fill/w:240/h:336/bg:fff/13/products/foo.jpg
```

therefore share one cached object:

```
13/_p/background=ffffff,height=336,resizing_type=fill,width=240/products/foo.jpg
```

An empty option set canonicalises to `_`. `CACHE_MODE` and the `X-Use-Cache`
header behave exactly as they do on the legacy routes.

### Status codes

| Code | When |
|------|------|
| `200` | Served, from cache or freshly processed |
| `403` | Signature missing, wrong, or `unsafe` without the opt-in |
| `400` | An option is unknown, malformed, or exceeds `MAX_DIMENSION` |
| `404` | Route disabled, source tail unrecognised, or the original is missing |
| `500` | The resize itself failed |

### Not supported

imgproxy's `plain/`, base64 and `enc/` source forms (sources are always
catalog-relative keys in the configured buckets), and the rest of its
vocabulary — `trim`, `pad`, `dpr`, `zoom`, `min-width`/`min-height`, `blur`,
`sharpen`, `watermark`, `preset`, `quality`, `format`, `/info`. Source keys
must not contain characters requiring percent-encoding.

## Worker trigger

`POST /_/worker/trigger` dispatches a bulk pre-resize batch to a detached
goroutine and returns `202 Accepted` immediately. The response is observable
before the batch starts.

Payload:

```json
{
  "clientId": "39",
  "version": "3",
  "images": ["catalog/products/images/foo.jpg", "catalog/products/images/bar.png"],
  "sizes": [[200, 0], [400, 0]],
  "formats": ["avif", "webp"]
}
```

Fields:

| Field | Required | Notes |
|-------|----------|-------|
| `clientId` | yes | Non-empty string. Becomes the leading segment of every output key (`{clientId}/{version}/images/products/...`). |
| `images` | yes | Non-empty array of fully-resolved S3 keys to original images. The proxy does no template substitution — resolve `{productUri}/{imageSrc}` upstream. |
| `formats` | yes | Non-empty array. Each entry must be one of `png`, `jpg`, `jpeg`, `webp`, `avif`. Invalid → 400 naming the offending value. |
| `sizes` | no | Array of `[width, height]` int pairs. Both values must be ≥ 0. When absent or `null` → fall back to the env `SIZES`. |
| `version` | no | String. Must parse as a non-negative integer (e.g. `"3"`). Absent / empty string → defaults to `3`. |

Output keys are written under:

```
{clientId}/{version}/images/products/{width}/{height}/{filename}.{format}
```

The cartesian product is `len(images) × len(sizes) × len(formats)`. For the example
above (1 image × 2 sizes × 2 formats) the batch produces 4 thumbnails.

Each output respects the storage topology configured via `CACHE_MODE` — in `shadow` or
`live` mode, every output is dual-written to both origin and cache, with per-side failures
logged independently.

The legacy `{"key": "..."}` payload is no longer accepted; callers must migrate to the
envelope before the new build ships, or they will receive 400.

## SVG & transparency

SVG is a supported **input** (original) format: an SVG original is rasterized to the
requested raster output format at the requested size. SVG is never emitted — there is
no SVG output format, and `svg` is not a valid value in a worker-trigger `formats`
array. (A stored `.svg` object requested by its exact key is still served verbatim by
the direct-serve path, unchanged by this.)

Transparency is handled by an alpha policy that defaults sensibly per source type and
can be overridden per request:

| Source | Output `png`/`webp`/`avif` | Output `jpg`/`jpeg` |
|--------|----------------------------|---------------------|
| SVG    | transparency **kept**      | flattened to white  |
| Raster (png, …) | flattened to white (unchanged historical behavior) | flattened to white |

`jpg`/`jpeg` have no alpha channel, so they always flatten regardless of source or override.

**Override segment.** An optional `flat/` or `alpha/` segment immediately before the
filename overrides the default:

```
.../products/{width}/{height}/flat/{filename}     # force flatten to white
.../products/{width}/{height}/alpha/{filename}    # force keep transparency
.../images/{folder}/alpha/{filename}              # also on the folder-image route
```

The segment is optional and additive — existing URLs are unaffected, and a real file
named `flat.png` or a folder named `flat` is **not** treated as an override (the
trailing slash disambiguates). Because the segment is part of the cache key, the
default, `flat/`, and `alpha/` variants of the same source cache independently.


Deprecated:
- `IMAGE_TAGS` — used to set S3 object tags. **Deprecated** as of the split-bucket
  refactor. Neither Hetzner Object Storage nor Cloudflare R2 implement the S3
  Tagging APIs, so the header was effectively silently dropped on HOS and would
  hard-fail on R2. If set, the proxy logs a single deprecation warning at
  startup and discards the value.

## Access log shape

Every request emits one JSON line to `stdout`. The shape mirrors the platform's
nginx access-log schema (so the same dashboards work across services), with two
additions specific to this Go origin:

- **`upstream.responseTime`** — the *sum* of all internal phase durations
  (S3 calls + libvips) in seconds. Always present.
- **`timings`** — a sparse map of per-phase wall-clock durations, also in
  seconds with 3-decimal precision. Same data as the `Server-Timing` response
  header, formatted for log shippers. The key is always present (`{}` on
  requests where no phases ran); only phases that actually executed appear
  inside. Phase keys are: `s3-get`, `resize`, `s3-put` (off-mode), and
  `s3-put-cache` / `s3-put-origin` (shadow/live modes). Future phase names
  added via `s.time(ctx, "...", ...)` flow through automatically.

`Server-Timing` response header uses milliseconds (per W3C); the JSON log uses
seconds (consistent with `request.time` and `upstream.responseTime`). Different
consumers, different conventions; same underlying data.
