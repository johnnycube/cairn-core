# 3. One distroless binary serves the API and the embedded web app

Status: accepted

## Context

Operators self-host Cairn. A separate Node web server would add a runtime, a deployment and a
reverse-proxy split.

## Decision

The SvelteKit app is built as a static SPA (adapter-static) and embedded into the Go binary with the
`embedweb` build tag. The release image (`docker/Dockerfile.core`) is a single static binary on
`gcr.io/distroless/static-debian12:nonroot`; `cairn serve` answers both the API and `/`.

## Consequences

No Node, shell or package manager in the runtime image. Server-side rendering is not available in
production; pages opt out of SSR. Local development still uses the Vite dev server with a proxy.
