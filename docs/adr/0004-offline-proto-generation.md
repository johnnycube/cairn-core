# 4. Proto code is generated offline with local plugins in a shared toolchain image

Status: accepted

## Context

`buf generate` with remote buf.build plugins hit BSR rate limits in CI and made output depend on
the network.

## Decision

`api/proto/buf.gen.yaml` uses only `local:` plugins. `docker/Dockerfile.build-base` pins Go, Node,
buf, protoc and every plugin, and is published as `ghcr.io/opencairn-org/cairn-build-base:1`. CI
(`make check`) and the release image build run inside it. The Go stubs in `gen/proto` are
committed so `go mod tidy` works on a clean checkout (Renovate runs it); `make proto-check` fails
when they drift. TypeScript stubs and OpenAPI output are regenerated, not committed.

## Consequences

Generation is deterministic and offline. A plugin version bump touches the build-base image, its
`:1` consumers and the Makefile pins together.
