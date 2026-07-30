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

Worker (bulk pre-resize defaults):
- `SIZES` — JSON array of target sizes (e.g. `[[150,210],[240,0]]`). Defaults to a predefined list of 33. Used as the fallback when a trigger payload omits `sizes`.
- `FORMAT` — historically the env-wide target format. **No longer used by the trigger** — the new payload requires `formats` explicitly. Kept as a field on the worker struct in case future internal callers need a default.

libvips tuning:
- `VIPS_CONCURRENCY`, `VIPS_MAX_CACHE_MEM`, `VIPS_MAX_CACHE_SIZE`.

Debug:
- `DEBUG=true` — enables libvips logging.

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
