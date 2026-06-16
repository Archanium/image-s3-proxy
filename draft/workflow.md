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

# Workflow

## Test Discipline

**Mode: Flexible.**

- Non-trivial changes require tests in the same change (preferred: same
  commit/PR).
- Pure refactors that don't change behavior may go without new tests if the
  existing test suite still covers the touched code.
- Strict TDD (test-before-implementation) is not required, but if a bug is
  reported, the workflow is **write a failing regression test first**, then
  fix.
- Test infrastructure conventions are documented in `draft/tech-stack.md`
  (Testing section). New tests should follow the existing mock idiom
  (struct of function pointers) — don't introduce a mocking library.

## Commits

- Conventional commit prefixes are the working style today (`fix:`, etc.).
  Continue with `feat:`, `fix:`, `refactor:`, `test:`, `chore:`, `docs:`.
- Keep commits scoped to a single concern. The recent history shows
  fix-by-fix granularity — preserve that.
- Don't batch unrelated changes.

## Code Review

- This is a single-maintainer project today. The maintainer reviews their own
  changes before merge.
- For non-trivial changes, run `/draft:review` (or `/code-review`) before
  pushing.

## Validation

- `make test` (Alpine) must pass before merging to `main`. CircleCI enforces
  this on the `build-and-push` job.
- For changes touching libvips behavior, **also** run `make test-debian` —
  libvips behavior can differ subtly between Alpine and Debian.
- For changes to the URL regex set in `server.go:17-21`: run the full
  `server_test.go` suite and add cases for both the new pattern and the
  existing patterns to confirm no regression.
- After any change to S3 read/write semantics: review with eyes on
  `architecture.md` §2 invariants I3–I7 (the cache contract and lazy
  migration).

## Branching & Merge

- Default branch: `main`.
- Direct commits to `main` are acceptable for the maintainer when running solo;
  for any larger change, use a feature branch and a PR.
- CircleCI only builds-and-pushes Docker images from `main`.

## Behavior Auto-Validate / Blocking

- **Auto-validate on commit**: not configured (no hooks). If introducing
  pre-commit hooks, make them advisory not blocking — the project ships a
  Docker-based test loop that is the source of truth.
- **Blocking on test failure**: yes, in CI. No image is pushed if tests fail.
- **Blocking on guardrail violation**: see `draft/guardrails.md` for the
  current hard-guardrail list.

## Quick Commands

```bash
make build          # build alpine app + run tests inside tester
make test           # run alpine tests
make test-debian    # run debian tests
make fmt            # go fmt ./...
make up             # docker compose up -d app
make down           # docker compose down
```
