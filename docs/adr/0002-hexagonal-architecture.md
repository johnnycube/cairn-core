# 2. Hexagonal architecture: domain, port, use case, adapter

Status: accepted

## Context

Cairn merges activities from several providers and stores them in Postgres, NATS and S3. The merge
engine and visibility rules must be testable without that infrastructure.

## Decision

- `internal/domain` is pure: it imports nothing from port, usecase or adapter.
- `internal/port` holds driven-port interfaces and imports only domain.
- `internal/usecase` orchestrates domain and ports and never imports adapters.
- Adapters (`internal/adapter/secondary/{postgres,nats,s3,inmem}`, `internal/adapter/primary/connect`)
  implement ports.
- `cmd/server/wire.go` is the only place concrete adapters are constructed and injected.

## Consequences

Domain and use-case tests run DB-free (`make test`); adapter behaviour is covered by the
`integration`-tagged suite against a real TimescaleDB+PostGIS. A layer crossing in review is sent
back for restructuring.
