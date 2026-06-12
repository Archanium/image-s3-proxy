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

# Guardrails

Rules that govern automated changes in this repo. Three tiers:

- **Hard Guardrails** — never violate; quality commands block on these.
- **Learned Conventions** — patterns observed in the codebase; follow unless
  there is a good reason to deviate.
- **Learned Anti-Patterns** — patterns to avoid; quality commands flag these.

---

## Hard Guardrails

### Git
- Never force-push `main`.
- Never use `--no-verify` on commits unless explicitly requested.
- Never commit `.env`, AWS keys, or anything matching `AWS_*KEY*` patterns.
- Don't rewrite published history (`main` after push).

### Code Quality
- All `.go` files must pass `gofmt`. CI does not currently enforce this; run
  `make fmt` before committing.
- New exported identifiers must have a doc comment when their purpose isn't
  obvious from the name (Go convention).
- No `panic()` in production code paths — error returns only. `log.Fatal` is
  acceptable in `main.go` for startup failures.

### Security
- Never log credentials. `s3.go:40` logs the **access key ID** (not the
  secret) — this is the upper bound; don't go further.
- Never reach for the AWS SDK directly outside `internal/s3`.
- The `POST /_/worker/trigger` endpoint has no auth — do not extend its
  surface (e.g., let it accept arbitrary URLs/buckets) without first adding
  auth, because the load-balancer-level controls assume a narrow attack
  surface.

### Testing
- New code paths that can fail (return an error, or change behavior under
  edge inputs) need at least one test exercising the failure case, not just
  the happy path. The existing suite is strong on this — preserve the bar.

---

## Learned Conventions

Patterns discovered in this codebase. Follow unless there's a reason not to.

### Project layout (Go standard)
- `cmd/<binary-name>/main.go` for executables.
- `internal/<package>/` for non-public packages.
- Tests live next to source as `<file>_test.go` in the same package.
- Test fixtures live in `tests/fixtures/`.

### Dependency wiring
- Constructor injection through `types.S3Client` / `types.Resizer` interfaces.
- No DI container, no service locator.
- `main.go` is the **only** layer that reads `os.Getenv`. Internal packages
  receive config as constructor arguments.

### Testing idiom
- **Mock pattern: struct of function pointers.** Example:
  ```go
  type mockS3Client struct {
      existsFunc func(ctx context.Context, key string) (bool, error)
      getFunc    func(ctx context.Context, key string) ([]byte, string, error)
      putFunc    func(ctx context.Context, key string, data []byte, contentType string, tags map[string]string) error
  }
  func (m *mockS3Client) Exists(ctx context.Context, key string) (bool, error) { return m.existsFunc(ctx, key) }
  ```
  Use this for any new interface. Do **not** introduce a mocking library
  (`gomock`, `testify/mock`).
- Table-driven tests for cases with many input variants.
- `TestMain` in each package silences `log` output unless `DEBUG=true`.
- HTTP handler tests use `httptest.NewRequest` + `httptest.NewRecorder`.

### Errors
- `fmt.Errorf("...: %w", err)` for wrapping when context matters.
- Plain `err` return otherwise (don't pre-emptively wrap if no caller will
  unwrap).
- `errors.Is` / `errors.As` if/when string-matching is needed —
  current code does string-`strings.Contains(err.Error(), "NotFound")`
  matching for S3 errors (`s3.go:84`), which is fragile. If a sentinel
  error becomes available in the SDK, prefer it.

### Logging
- `log.Printf` everywhere; key=value-ish format (`"bucket=%s, key=%s"`).
- One log line per significant decision point in a request lifecycle
  (existence check, fallback consult, normalization).
- Errors at the request layer get logged AND returned via `httpError`.

### HTTP response shaping
- Always set `Content-Type` before writing the body.
- Always set `Cache-Control` (long max-age on success, `max-age=30` on
  errors via `httpError`).
- Use `httpError` helper, not raw `http.Error`, so the cache-control header is
  set consistently.

### S3 surface
- `S3Client` interface methods take `context.Context` as the first arg.
- Tagging is `map[string]string` and is URL-query-escaped at the SDK boundary.
- Reads consult the fallback bucket; writes do not.

### Concurrency
- Goroutines must have a documented lifetime owner. The only fire-and-forget
  goroutine in the codebase is `server.handleWorkerTrigger:293-298` — it is
  explicitly called out in `architecture.md` §2 (I10) and §9 (gap #5).
- No `sync.Mutex` / `sync.RWMutex` in production code today. Adding one
  warrants a comment explaining the contended state.

---

## Learned Anti-Patterns

Patterns to avoid in this codebase. Quality commands should flag these.

### Cache layer
- **Do not** add an in-process LRU/byte cache on top of S3. The bucket is the
  cache (architecture.md I3). Adding a second layer breaks lifecycle/cost
  assumptions and creates a cache-coherency problem for the put-back path.

### Configuration
- **Do not** call `os.Getenv` outside `cmd/image-proxy/main.go`. All config
  flows through constructors.
- **Do not** introduce a "Config" struct as a hidden global — pass values
  explicitly.

### Hot path I/O
- **Do not** add unconditional `s3.Put` calls in new request handlers without
  considering write-cost amplification — every `Put` on a cache-miss path
  costs an S3 write. Audit any pattern that could result in repeated misses
  for the same key.

### libvips
- **Do not** call `vips.Startup` or `vips.Shutdown` outside `LibvipsResizer`.
  libvips state is process-global.
- **Do not** forget `defer image.Close()` after `vips.LoadImageFromBuffer`.
  Each `ImageRef` owns native memory.

### URL routing
- **Do not** add HTTP routing logic outside `server.ServeHTTP`. The dispatch
  ladder there is the contract.
- **Do not** introduce a router framework (`chi`, `gorilla/mux`, etc.)
  without a separate scoping discussion — the regex ladder is the deliberate
  shape.

### Hard-coded tenant
- `worker.go:78` hard-codes `clientId=13` in the thumbnail key template.
  This is a known limitation (architecture.md §9 #2). **Do not** copy this
  pattern; if you touch worker code, treat that constant as a TODO and
  thread the clientId from the trigger payload or the matched key.

### Logging
- **Do not** log AWS secret keys, AWS_SECRET_ACCESS_KEY env values, or any
  contents of the `Tagging` header that includes credentials.

### Testing
- **Do not** use real network calls in `_test.go`. Always mock the S3
  interface. The fixtures in `tests/fixtures/` are PNG bytes the resizer can
  consume; that's the maximum I/O a unit test should do.

### Dead code
- `types.Storage` interface is declared but unused. Don't take a dependency
  on it. Either wire it intentionally or delete it in a separate change.
- `worker.destS3Client` parameter is plumbed through `NewWorker` but always
  passed `nil` from the server constructor. Same — don't quietly start
  using it without an env-var to populate it.
