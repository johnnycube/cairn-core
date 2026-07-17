package nats

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnnycube/cairn-core/internal/adapter/secondary/inmem"
)

// helper: spin up an inmem KV bucket and a rate-limiter on top.
func newTestLimiter(t *testing.T, capacities map[string]int) *RateLimiter {
	t.Helper()
	bus := inmem.New()
	kv, err := bus.KV("cairn_rate_limits")
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	rl := NewRateLimiter(kv, capacities)
	rl.sleep = func(time.Duration) {} // no real backoff in tests (deterministic + fast)
	return rl
}

func TestReserve_AllowsUntilCapacity(t *testing.T) {
	rl := newTestLimiter(t, map[string]int{"strava:short": 3})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ok, retryAfter, err := rl.Reserve(ctx, "strava:short", 1)
		if err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Reserve %d: expected ok=true, got false (retry=%v)", i, retryAfter)
		}
	}

	// Fourth call should be denied with a retry-after.
	ok, retryAfter, err := rl.Reserve(ctx, "strava:short", 1)
	if err != nil {
		t.Fatalf("Reserve 4: %v", err)
	}
	if ok {
		t.Fatal("Reserve 4: expected ok=false")
	}
	if retryAfter <= 0 || retryAfter > 16*time.Minute {
		t.Errorf("Reserve 4: retryAfter = %v, want between 0 and 16m", retryAfter)
	}
}

func TestReserve_UnknownBucket_PermissiveDefault(t *testing.T) {
	// Empty capacities map → unknown bucket → allow everything.
	rl := newTestLimiter(t, map[string]int{})
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		ok, _, err := rl.Reserve(ctx, "totally-unknown:bucket", 1)
		if err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Reserve %d on unknown bucket should have been allowed", i)
		}
	}
}

func TestReserve_WindowRollover(t *testing.T) {
	// Build a limiter with controllable clock so we can fast-forward.
	rl := newTestLimiter(t, map[string]int{"strava:short": 2})
	var clock atomicTime
	clock.Set(time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	rl.now = clock.Get

	ctx := context.Background()

	// Exhaust the bucket.
	for i := 0; i < 2; i++ {
		ok, _, err := rl.Reserve(ctx, "strava:short", 1)
		if err != nil || !ok {
			t.Fatalf("warm-up reserve %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, _, _ := rl.Reserve(ctx, "strava:short", 1)
	if ok {
		t.Fatal("bucket should be exhausted")
	}

	// Advance clock past the 15-min short-window — next Reserve should
	// see the reset and grant.
	clock.Set(clock.Get().Add(16 * time.Minute))
	ok, _, err := rl.Reserve(ctx, "strava:short", 1)
	if err != nil {
		t.Fatalf("post-rollover reserve: %v", err)
	}
	if !ok {
		t.Fatal("post-rollover reserve: expected ok=true after window expired")
	}
}

func TestForceRefill_OverridesAvailableImmediately(t *testing.T) {
	rl := newTestLimiter(t, map[string]int{"strava:short": 200})
	ctx := context.Background()

	// Take 50 tokens.
	for i := 0; i < 50; i++ {
		if ok, _, err := rl.Reserve(ctx, "strava:short", 1); err != nil || !ok {
			t.Fatalf("warm-up %d: %v", i, err)
		}
	}

	// Provider says "you're at 0/200, reset in 10 min".
	resetAt := time.Now().Add(10 * time.Minute)
	if err := rl.ForceRefill(ctx, "strava:short", 0, resetAt); err != nil {
		t.Fatalf("ForceRefill: %v", err)
	}

	// Next reserve must be denied — bucket fully consumed by force-refill.
	ok, retryAfter, err := rl.Reserve(ctx, "strava:short", 1)
	if err != nil {
		t.Fatalf("post-force reserve: %v", err)
	}
	if ok {
		t.Fatalf("after ForceRefill(available=0), Reserve must be denied; got ok=true")
	}
	if retryAfter <= 0 || retryAfter > 11*time.Minute {
		t.Errorf("retryAfter = %v, want roughly 10m", retryAfter)
	}
}

func TestSnapshot_ReportsState(t *testing.T) {
	rl := newTestLimiter(t, map[string]int{"strava:short": 200})
	ctx := context.Background()

	// New bucket: empty state.
	snap, err := rl.Snapshot(ctx, "strava:short")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Bucket != "strava:short" {
		t.Errorf("bucket = %q", snap.Bucket)
	}

	// After 7 reservations, Available should reflect them.
	for i := 0; i < 7; i++ {
		if _, _, err := rl.Reserve(ctx, "strava:short", 1); err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
	}
	snap, err = rl.Snapshot(ctx, "strava:short")
	if err != nil {
		t.Fatalf("Snapshot post-reserve: %v", err)
	}
	if snap.Capacity != 200 {
		t.Errorf("capacity = %d, want 200", snap.Capacity)
	}
	if snap.Available != 193 {
		t.Errorf("available = %d, want 193 (200 - 7)", snap.Available)
	}
}

func TestReserve_CASContention_Recovers(t *testing.T) {
	// Spin up N concurrent goroutines that all try to drain the bucket.
	// Exactly `capacity` of them should win; the rest get denied. Tests
	// that the CAS-retry loop converges under contention without losing
	// updates or double-counting.
	rl := newTestLimiter(t, map[string]int{"strava:short": 50})
	ctx := context.Background()

	const N = 200
	var (
		granted int
		denied  int
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := rl.Reserve(ctx, "strava:short", 1)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("Reserve error: %v", err)
				return
			}
			if ok {
				granted++
			} else {
				denied++
			}
		}()
	}
	wg.Wait()

	if granted != 50 {
		t.Errorf("granted = %d, want exactly 50", granted)
	}
	if denied != N-50 {
		t.Errorf("denied = %d, want %d", denied, N-50)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// atomicTime is a simple mutex-protected clock for tests. We don't use
// atomic.Value because time.Time isn't a single word and value-store
// types in atomic require the SAME concrete type on every Store.
type atomicTime struct {
	mu sync.Mutex
	t  time.Time
}

func (a *atomicTime) Set(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.t = t
}

func (a *atomicTime) Get() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}

// TestReserve_NoErrorUnderExtremeContention asserts that persistent CAS
// contention surfaces as a soft (false, delay, nil) — NOT an error — so the
// worker backs off via NakWithDelay instead of busy-looping (the production
// livelock we hit). With a tiny capacity and many contenders, the losers must
// be cleanly denied, never error.
func TestReserve_NoErrorUnderExtremeContention(t *testing.T) {
	rl := newTestLimiter(t, map[string]int{"strava:short": 1})
	ctx := context.Background()

	const N = 300
	var (
		granted, denied, errored int
		mu                       sync.Mutex
		wg                       sync.WaitGroup
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, delay, err := rl.Reserve(ctx, "strava:short", 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				errored++
			case ok:
				granted++
			default:
				denied++
				if delay < 0 {
					t.Errorf("denied reservation returned negative delay %v", delay)
				}
			}
		}()
	}
	wg.Wait()

	if errored != 0 {
		t.Errorf("contention surfaced as an error %d times; must be a soft deny+delay", errored)
	}
	if granted != 1 {
		t.Errorf("granted = %d, want exactly 1 (capacity 1)", granted)
	}
	if denied != N-1 {
		t.Errorf("denied = %d, want %d", denied, N-1)
	}
}

// TestCASBackoff_Bounded sanity-checks the jittered backoff is non-negative and
// capped.
func TestCASBackoff_Bounded(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		d := casBackoff(attempt)
		if d < 0 || d > 20*time.Millisecond {
			t.Fatalf("casBackoff(%d) = %v out of [0,20ms]", attempt, d)
		}
	}
}
