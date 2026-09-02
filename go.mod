module github.com/johnnycube/cairn-core

// Go 1.26 is the current stable as of May 2026 (released Feb 2026).
// Go 1.22 went EOL when 1.24 dropped; the support policy is "two most
// recent majors". Upgrading also unlocks the Green-Tea GC (default in
// 1.26), -30% cgo overhead (relevant if/when we add a FIT parser via
// cgo), and the new `new(expr)` allocator syntax that we use in a few
// recompute paths.
go 1.26

require (
	// Direct dependencies pinned to current stable as of May 2026.
	// After fresh checkout run `go mod tidy` to resolve indirect deps.

	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/jwt/v2 v2.8.2

	// NATS stack — added for the worker-control-plane + dynamic
	// enrollment work (see docs/architecture.md §1-§5 + §4.5).
	//
	//   nats.go    : Go client (Connect, JetStream, KV, ObjectStore)
	//   nkeys      : Ed25519 NKey primitives for the auth-callout flow
	//   jwt/v2     : JWT minting + verification for NATS account/user JWTs
	//
	// Versions are the latest stable as of May 2026.
	github.com/nats-io/nats.go v1.53.1
	github.com/nats-io/nkeys v0.4.16
	github.com/pressly/goose/v3 v3.27.3
	github.com/spf13/cobra v1.10.2
	github.com/vgarvardt/pgx-google-uuid/v5 v5.6.0
	golang.org/x/crypto v0.55.0
)

require (
	connectrpc.com/connect v1.20.0
	github.com/aws/aws-sdk-go-v2 v1.45.1
	github.com/aws/aws-sdk-go-v2/credentials v1.20.2
	github.com/aws/aws-sdk-go-v2/service/s3 v1.110.0
	github.com/aws/smithy-go v1.28.1
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/go-webauthn/webauthn v0.18.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/prometheus/client_golang v1.24.1
	github.com/tormoder/fit v0.15.0
	golang.org/x/oauth2 v0.36.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/client9/misspell v0.3.4 // indirect
	github.com/fxamacker/cbor/v2 v2.9.3 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.3.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/gordonklaus/ineffassign v0.0.0-20210914165742-4cc7213b9bc8 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kisielk/errcheck v1.6.1 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/mdempsky/unconvert v0.0.0-20230125054757-2661c2c99a9b // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20221208152030-732eee02a75a // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260708182218-49f421fb7959 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	golang.org/x/tools/go/expect v0.1.1-deprecated // indirect
	honnef.co/go/tools v0.4.2 // indirect
	mvdan.cc/gofumpt v0.4.0 // indirect
)

// After fresh checkout run:
//
//   make tidy   (or: go mod tidy)
//
// to resolve indirect dependencies (pflag, the pgx internals, goose's
// dialect driver bindings, NATS protobuf encoders, etc.) into the
// require block. The build container has no proxy.golang.org access so
// this happens at the developer machine.
//
// Library version audit policy: bumped during the May-2026 NATS
// integration milestone. Re-audit on every minor version bump of Go
// (every 6 months) — see docs/architecture.md for the audit table.
