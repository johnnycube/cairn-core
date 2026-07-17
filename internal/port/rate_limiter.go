package port

import (
	"context"
	"time"
)

// RateLimiter is the bucket-based reservation interface workers use
// before every external API call. Implementations live in the
// adapter layer; the canonical Cairn implementation is NATS-KV-backed
// (see docs/architecture.md §6) so multiple worker instances of the
// same provider share one global counter.
//
// Buckets are namespaced strings. Convention:
//
//	<provider>:short    — short rolling window (e.g., Strava 200/15min)
//	<provider>:daily    — daily quota (e.g., Strava 2000/day)
//	<provider>:webhook  — separate budget for webhook-triggered fetches
//	                      so they don't starve under heavy backfill load
//
// Callers typically Reserve from BOTH short and daily before an API
// call and wait the longer of the two retryAfter durations.
type RateLimiter interface {
	// Reserve takes `tokens` from the bucket. Returns:
	//   - ok=true:  permission granted, caller proceeds.
	//   - ok=false: bucket empty; retryAfter is the wait until refill.
	//
	// Implementations are safe under concurrent calls from multiple
	// worker instances sharing the same bucket — they use atomic
	// compare-and-set on the underlying KV (or a Redis equivalent).
	Reserve(ctx context.Context, bucket string, tokens int) (ok bool, retryAfter time.Duration, err error)

	// ForceRefill is called after a 429 from the upstream API with
	// authoritative information from response headers. Sets the bucket
	// to the given `available` token count and the server-reported
	// reset time. All workers will observe the new state on their
	// next Reserve call.
	ForceRefill(ctx context.Context, bucket string, available int, windowResetsAt time.Time) error

	// Snapshot returns the current bucket state for operator visibility.
	// Implementations may return an approximation under high concurrency
	// — this is not part of the reservation hot path.
	Snapshot(ctx context.Context, bucket string) (BucketSnapshot, error)
}

type BucketSnapshot struct {
	Bucket          string
	Available       int
	Capacity        int
	WindowResetsAt  time.Time
	LastReservation time.Time
}
