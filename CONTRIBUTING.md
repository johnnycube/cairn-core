# Contributing to Cairn

Thank you for your interest in Cairn. This document covers what you need to
know to file a useful issue or open a PR that has a chance of being merged.

## TL;DR

1. **Open an issue first** for non-trivial changes so we can agree on direction
   before you invest hours.
2. **Sign off your commits** with `git commit -s`. We use the
   [Developer Certificate of Origin](https://developercertificate.org) — no
   separate CLA paperwork.
3. **Run `make lint test` before pushing.** CI runs the same checks.
4. **Keep PRs small and focused.** One change per PR. Big refactors get
   split into reviewable chunks.

## Development setup

Prerequisites:

- Go 1.26 or newer
- Docker (for the Postgres+TimescaleDB+PostGIS+MinIO+NATS dev stack)
- `make`
- `golangci-lint` (CI uses the version pinned in `.github/workflows/ci.yml`)

Quick start:

```sh
# Clone and bring up infra
git clone https://github.com/johnnycube/cairn-core
cd cairn
make dev-up        # docker compose -f docker-compose.dev.yml up -d

# Build the server binary
make build

# Apply schema migrations
./bin/cairn migrate up

# Run the server (default :8080)
./bin/cairn serve
```

The README's "Smoketest" section walks through end-to-end usage with `curl`
once the server is up.

## Code style and architecture

**Read `CLAUDE.md` first.** It documents the hexagonal-architecture rules
that the whole codebase is built around. The short version:

- `domain/` is pure. No imports from `port/`, `usecase/`, or `adapter/`.
- `port/` defines interfaces. Imports `domain/` only.
- `usecase/` implements application logic. Imports `domain/` and `port/`.
- `adapter/secondary/postgres/` implements `port` interfaces. Imports `domain/`.
- `cmd/server/wire.go` is the **only** place concrete adapters are
  constructed and injected into use cases.

If a PR violates a layer rule, it'll be sent back for restructuring — not
because of dogma, but because every layer crossing has paid for itself in
testability and refactor speed.

Other conventions:

- **Typed UUIDs** (`domain.UserID`, `ActivityID`, …) — never naked `uuid.UUID`
  in function signatures crossing module boundaries.
- **`errors.Is(err, domain.ErrNotFound)`** — sentinel errors, not string
  matching.
- **`uc.tx.InTx(ctx, fn)` for multi-write transactions** — never grab a
  `*pgx.Tx` directly from inside a use case.
- **`gofumpt`** (not just `gofmt`) — stricter formatting, no debate.
- **`slog`** (standard library) — no Zap, Logrus, or other logging libs.
- **No ORM.** Hand-written SQL in `adapter/secondary/postgres/`. The schema
  uses features (PostGIS, JSONB operators, partial indexes, window functions)
  that don't survive ORM round-tripping anyway.

## Adding a dependency

We're conservative with dependencies. Before opening a PR that pulls in a
new module, please:

1. Check it has an active maintainer and a healthy issue tracker.
2. Verify it's MIT, BSD-2/3-Clause, Apache-2.0, or MPL-2.0. We keep
   dependencies permissive even though the core is AGPL-3.0 — it keeps
   licensing simple and our options open.
3. Justify it in the PR description: what does it do that stdlib can't,
   and why is the cost worth it?

A `go.mod` line item is a maintenance commitment in perpetuity. The bar
is "this saves more developer time than it will cost over five years".

## Pull request process

1. Fork, branch off `main`, push your branch to your fork.
2. Open a PR against `cairn-app/cairn:main`. Fill in the template.
3. CI must pass (lint + test + build). Maintainers won't review red PRs.
4. Address review feedback by amending or pushing follow-up commits.
   We squash on merge, so commit-history-grooming on your branch is
   optional.
5. Once approved, a maintainer merges.

### Commit messages

Subject line: imperative mood, ≤ 72 chars, no trailing period. Body
explains *why* not *what* (the diff shows what).

Bad:  `Updated the merge engine`
Good: `Drop the per-source priority fallback when policy gives ties`

### DCO sign-off

Every commit needs a `Signed-off-by:` trailer, added automatically by
`git commit -s`. The DCO is a lightweight assertion that you have the
right to contribute the code you're submitting under the project's
license. Full text at <https://developercertificate.org>.

CI checks DCO sign-off and fails the build if any commit lacks it.

## Testing

```sh
make test         # unit tests (no DB required)
make test-race    # unit tests with -race
make lint         # golangci-lint
make tidy         # go mod tidy + go mod verify
```

The pure-domain and pure-use-case tests run without infrastructure. The
DB-backed adapter tests will (once added) require the docker-compose
stack to be up.

## Reporting bugs

Use the **Bug Report** issue template. Include:

- Cairn version (`./bin/cairn version`)
- Go version (`go version`)
- Postgres / TimescaleDB / PostGIS versions
- Minimal reproduction (curl commands or a short Go program)
- Relevant logs (set `CAIRN_LOG_LEVEL=debug` for verbose output)

## Reporting security issues

**Do not file a public issue for security problems.** See `SECURITY.md`
for the responsible-disclosure process.

## Community

- **Questions / discussion:** GitHub Discussions
- **Bug reports / feature requests:** GitHub Issues with the appropriate template
- **Real-time chat:** *coming once contributor count justifies it*

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
By participating you agree to abide by its terms.
