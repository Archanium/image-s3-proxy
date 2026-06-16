---
project: "image-s3-proxy"
module: "root"
generated_by: "draft:init refresh"
generated_at: "2026-06-16T08:20:05Z"
git:
  branch: "main"
  remote: "origin/main"
  commit: "6f2e71a3c70d732b573801747ca0435698c9d0e5"
  commit_short: "6f2e71a"
  commit_date: "2026-06-16 00:48:55 +0200"
  commit_message: "chore: bring draft/tracks.md frontmatter up to current HEAD"
  dirty: false
synced_to_commit: "6f2e71a3c70d732b573801747ca0435698c9d0e5"
---

# Product

## Vision

On-demand image transformation proxy for an e-commerce platform. Serves resized
product, block, and branding imagery from S3-compatible storage. Mirrors the URL
contract of a prior Node.js implementation so existing webshop frontends keep
working without coordinated client changes.

## Users

| User | Primary need |
|------|--------------|
| Webshop frontend (browser) | Fast image fetches at arbitrary sizes, behind a CDN, with long-lived cache headers |
| Webshop backend / catalog admin | Bulk pre-resize of newly uploaded originals via the `POST /_/worker/trigger` endpoint |
| Ops / platform team | A small, single-binary service that runs behind a load balancer and emits stdout logs |

## Goals

- **Compatibility:** preserve the URL contract from the Node.js predecessor so
  no frontend change is needed.
- **Cost-efficient cache:** reuse the S3 bucket as the cache tier; avoid any
  in-process LRU/disk cache.
- **Operational simplicity:** single binary, env-var config, Docker images for
  both Alpine and Debian (libvips compatibility).
- **Lazy migration:** support reading from a legacy S3 bucket as fallback and
  copy-on-read into the primary bucket.

## Features

### P0 — must have (already shipped)

- `GET /{key}` serves any existing object from S3 directly.
- `GET /{key}` with one of three URL patterns triggers on-the-fly resize via
  libvips when the cached variant is missing.
- Resized variants are cached back to S3 under the normalized key.
- Optional fallback S3 client for lazy migration from a legacy bucket.
- `POST /_/worker/trigger` for bulk pre-resize of a single original across all
  `DefaultSizes` (or `SIZES` env override).
- Format conversion: png / jpg / webp / avif.

### P1 — should have

- Per-clientId worker key construction (today hard-coded `clientId=13` in
  `worker.go:78`).
- Auth or IP allowlisting on `POST /_/worker/trigger` (today relies on
  load-balancer-level controls).
- Observability beyond `log.Printf` — at minimum request count / latency / 5xx
  rate.

### P2 — nice to have

- Batch migration command for the fallback bucket so the lazy-migration path
  isn't the only way old objects move.
- Pluggable storage backend (the unused `types.Storage` interface hints at this).
- Per-tenant tag overrides instead of the single `IMAGE_TAGS` env.

## Success Criteria

- Existing webshop frontends continue to work after deploy with no client
  changes (URL contract preserved).
- p95 latency for cache-hit requests dominated by S3 GET, not by Go overhead.
- Resize errors do not block the response of a separate, valid request
  (one bad asset cannot cascade).
- Lazy migration converges: objects requested repeatedly from the fallback
  bucket eventually live in the primary bucket.

## Constraints

- Runs in production today; URL contract is frozen for non-breaking work.
- libvips is a CGO dependency; the binary cannot be `CGO_ENABLED=0`.
- Builds and tests must work in both Alpine and Debian containers
  (libvips package availability differs between them).
- No coordinated frontend change is possible without separate scoping.

## Non-Goals

- Image editing beyond resize / format conversion (no cropping by user-supplied
  coordinates, no filters, no watermarking).
- Authentication of end-user requests (delegated to the CDN / load balancer).
- A web UI or admin dashboard.
- General-purpose CDN behavior (this service is e-commerce specific and bakes
  catalog-folder semantics — `products`, `blocks`, `branding` — into its
  routing).
- Multi-region replication / failover beyond what AWS SDK + the configured
  endpoint provides.

## Stakeholders

- Owner: thomas@kasasagi.dk
- Deployment: CircleCI → Docker Hub (`archanium/image-s3-proxy`).
