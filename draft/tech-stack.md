---
project: "image-s3-proxy"
module: "root"
generated_by: "draft:init"
generated_at: "2026-06-12T12:41:43Z"
git:
  branch: "main"
  remote: "origin/main"
  commit: "21fdfb3c6f1fc77bc3f10c54bd56e804e5ff0d86"
  commit_short: "21fdfb3"
  commit_date: "2026-05-07 18:46:19 +0200"
  commit_message: "fix: updating the tests and fixing an error on the normalized key"
  dirty: false
synced_to_commit: "21fdfb3c6f1fc77bc3f10c54bd56e804e5ff0d86"
---

# Tech Stack

## Language & Runtime

| Layer | Choice | Source |
|-------|--------|--------|
| Language | Go 1.19 | `go.mod:3` |
| CGO | Required (libvips) | `Dockerfile.alpine`, `govips` import |

## Production Dependencies (`go.mod`)

| Module | Version | Role |
|--------|---------|------|
| `github.com/aws/aws-sdk-go-v2` | v1.17.4 | AWS SDK core |
| `github.com/aws/aws-sdk-go-v2/config` | v1.18.12 | SDK config loader (region, creds, endpoint resolver) |
| `github.com/aws/aws-sdk-go-v2/credentials` | v1.13.12 | Static credentials provider |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.30.2 | S3 HeadObject / GetObject / PutObject |
| `github.com/davidbyttow/govips/v2` | v2.16.0 | libvips bindings for resize/encode |

Indirect deps are SDK internals (`smithy-go`, `eventstream`, `imds`, etc.) and
`golang.org/x/image|net|text`. No web framework, no router library, no logger
library, no observability library, no test framework — stdlib only.

## HTTP

- `net/http` stdlib.
- No router framework; routing happens via three `regexp.MustCompile` patterns
  in `internal/server/server.go:17-21` plus a special case for
  `POST /_/worker/trigger`.
- Server is started with `http.ListenAndServe(":"+port, srv)` —
  **no explicit timeouts are configured today** (deviates from
  `junie.md` style guide; left as P1 hardening).

## Storage

- S3 (AWS SDK v2). Supports custom `S3_ENDPOINT` for Hetzner Object Storage,
  MinIO, etc.
- Optional **fallback bucket** for migration (read-through, copy-on-read).
- Cache layer = the primary bucket itself. No in-process or disk cache.

## Image Processing

- libvips via `govips/v2`.
- libvips global lifecycle managed by `LibvipsResizer.Startup` /
  `LibvipsResizer.Shutdown` (called once each from `main.go`).
- Supported output formats: `png`, `jpg`/`jpeg`, `webp`, `avif`.
  Default fallback in resizer is JPEG.

## Configuration

Process environment only. No config file, no flag parsing, no hot reload.
Full env-var catalog lives in `architecture.md` §7.2 and `.ai-context.md`
`CONFIG` section.

## Build & Packaging

- **Makefile** wraps Docker Compose for build/test/run.
- **Two Docker images** published per release: `alpine-latest` and
  `bookworm-latest` (each platform: linux/amd64, linux/arm64).
- Multi-stage Docker builds; final stage runs as non-root with
  `CGO_ENABLED=1` (libvips dependency).

## CI/CD

- **CircleCI** (`.circleci/config.yml`):
  - `test` job — runs Alpine tester + Debian tester (`go test -v ./...`).
  - `build-and-push` job — gated on `test`, only on `main`, only with
    `docker_auth` context, pushes to Docker Hub.

## Testing

- **`testing` stdlib**, no framework.
- **`net/http/httptest`** for HTTP handlers (`server_test.go`).
- **Mock pattern**: struct-of-function-pointers
  (e.g. `mockS3Client{existsFunc, getFunc, putFunc}` in
  `server_test.go:25-50`). This is the project's idiom — preserve it in
  new tests.
- **Table-driven** tests are the standard form for cases with many input
  variants (see `server_test.go`).
- `TestMain` in each package silences `log` output unless `DEBUG=true` is set
  in the environment.
- Fixtures live in `tests/fixtures/`.

## Local Development

- `make test` → docker compose tester (Alpine, with libvips-dev preinstalled).
- `make test-debian` → same but Debian.
- `make up` → docker compose `app` service.
- `go test ./...` works directly if libvips-dev is installed on the host
  (Linux / macOS via Homebrew).
- `make fmt` → `go fmt ./...`.

## Accepted Patterns

- Constructor injection of dependencies through `types.Resizer` / `types.S3Client`
  interfaces — keep this so tests can mock cleanly.
- `log.Printf` for all logging; no structured logger today. If introducing
  structured logging, replace it project-wide rather than per-package.
- All public S3 surface goes through `types.S3Client`; don't reach for the AWS
  SDK directly outside `internal/s3`.

## Patterns to Avoid (Anti-patterns)

- **No in-process byte cache** on top of S3 — the bucket is the cache. Adding
  one breaks the cost / lifecycle assumptions.
- **No direct AWS SDK calls** outside `internal/s3`.
- **No new env-var globals** read from `os.Getenv()` outside `main.go` — all env
  reads belong at the bootstrap layer.
- **No goroutines without an explicit context**, except the existing
  fire-and-forget worker trigger (which is acknowledged in §9 of
  `architecture.md`).
- **No `vips.Startup` from anywhere other than `LibvipsResizer.Startup`** —
  libvips is global state.
