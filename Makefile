# Cairn — top-level Makefile
#
# Standard developer workflow on a fresh checkout:
#
#   make tools         # one-time: install go-side dev tools to ./bin/
#   make proto         # generate proto stubs into ./gen/proto/ (needs buf)
#   make tidy          # resolve indirect Go dependencies
#   make build         # build the cairn binary into ./bin/cairn
#   make test          # run unit tests
#   make migrate-up    # apply pending DB migrations (needs CAIRN_DATABASE_URL)
#
# Required tools (install yourself, not via this Makefile):
#   * Go 1.22+
#   * buf >= 1.40 — https://buf.build/docs/installation
#   * Postgres 16 + TimescaleDB 2.x + PostGIS for actual runs

GO        ?= go
BUF       ?= buf
BIN_DIR   := bin
PKG       := github.com/johnnycube/cairn-core

LDFLAGS := -X main.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all
all: build

# ----------------------------------------------------------------------------
# Build
# ----------------------------------------------------------------------------

.PHONY: build
build:
	$(GO) build -ldflags='$(LDFLAGS)' -o $(BIN_DIR)/cairn ./cmd/server

.PHONY: install
install:
	$(GO) install -ldflags='$(LDFLAGS)' ./cmd/server

# ----------------------------------------------------------------------------
# Tests
# ----------------------------------------------------------------------------

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

# Integration tests run against a real Postgres (TimescaleDB+PostGIS). Set
# CAIRN_TEST_DATABASE_URL to a throwaway DB; the suite runs all migrations
# against it first. Build-tagged `integration` so plain `make test` stays
# DB-free.
.PHONY: test-integration
test-integration:
	$(GO) test -tags integration ./...

.PHONY: vet
vet:
	$(GO) vet ./...

# fmt-check fails if any Go file isn't gofmt-clean (used by CI).
.PHONY: fmt-check
fmt-check:
	@unformatted="$$(gofmt -l $$(find . -name '*.go' -not -path './web/*'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

# ci runs the same DB-free gates as the CI workflow's Go job.
.PHONY: ci
ci: fmt-check vet test
	$(GO) build ./...

# ----------------------------------------------------------------------------
# Dependencies
# ----------------------------------------------------------------------------

.PHONY: tidy
tidy:
	$(GO) mod tidy

# ----------------------------------------------------------------------------
# Proto generation
#
# Generates:
#   gen/proto/cairn/v1/*.go               (Go message types + Connect handlers)
#   gen/proto/cairn/worker/v1/*.go
#   web/src/lib/proto/                    (TypeScript types + Connect-Web clients)
#   gen/openapi/openapi.yaml              (OpenAPI v3)
#
# Provider-worker stubs are generated in their own repos now (Go for
# cairn-provider-strava, Python for cairn-provider-garmin) — not here.
#
# The Go server code under internal/adapter/primary/connect/ imports the
# generated Go packages and will not compile until `make proto` has been run.
# ----------------------------------------------------------------------------

.PHONY: proto
proto:
	cd api/proto && $(BUF) generate

.PHONY: proto-lint
proto-lint:
	cd api/proto && $(BUF) lint

.PHONY: proto-format
proto-format:
	cd api/proto && $(BUF) format -w

# ----------------------------------------------------------------------------
# Database migrations (delegates to the cairn binary's `migrate` subcommand)
# ----------------------------------------------------------------------------

.PHONY: migrate-up
migrate-up:
	$(GO) run ./cmd/server migrate up

.PHONY: migrate-status
migrate-status:
	$(GO) run ./cmd/server migrate status

.PHONY: migrate-redo
migrate-redo:
	$(GO) run ./cmd/server migrate redo

# ----------------------------------------------------------------------------
# Local runtime
# ----------------------------------------------------------------------------

.PHONY: run
run:
	$(GO) run ./cmd/server serve

# ----------------------------------------------------------------------------
# Local dev — supporting services in docker compose, binaries on the host with
# live-reload. Four shells:
#
#   make dev-up                  # postgres + nats + minio + mailpit (compose)
#   cp dev.env.example dev.env   # once; tweak if needed
#   make dev-server              # shell 2: Go server, rebuilds on save (air)
#   make dev-web                 # shell 3: SvelteKit dev server (vite HMR)
#   make dev-down                # stop services (add ARGS=-v to wipe data)
#
# Provider workers live in their own repos now (cairn-provider-strava,
# cairn-provider-garmin) — run them from there against this dev stack's NATS.
#
# dev-server sources dev.env (if present) so the binary picks up the compose
# connection strings + dev secrets. air is fetched on first run via `go run`
# — no separate install.
# ----------------------------------------------------------------------------
DEV_COMPOSE := docker compose -f docker-compose.dev.yml
AIR         := $(GO) run github.com/air-verse/air@latest

.PHONY: dev-up dev-down dev-logs dev-server dev-web
dev-up:
	$(DEV_COMPOSE) up -d

dev-down:
	$(DEV_COMPOSE) down $(ARGS)

dev-logs:
	$(DEV_COMPOSE) logs -f

dev-server:
	set -a; [ -f dev.env ] && . ./dev.env; set +a; $(AIR) -c .air.server.toml

dev-web:
	set -a; [ -f dev.env ] && . ./dev.env; set +a; cd web && npm run dev

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
