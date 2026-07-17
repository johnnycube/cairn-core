package main

import (
	"sync"
	"time"
)

// ipRateLimiter is a small in-memory token-bucket limiter keyed by client IP.
// It bounds request rate on unauthenticated endpoints (the webhook forwarder)
// so a single source can't flood JetStream. Not distributed — each server
// instance limits independently, which is sufficient as a coarse abuse cap.
type ipRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*ipBucket
	capacity float64 // max tokens (burst)
	refill   float64 // tokens added per second
	now      func() time.Time
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

// newIPRateLimiter allows `perMinute` requests/min per IP with a burst up to
// `perMinute` (full bucket). now may be nil (uses time.Now).
func newIPRateLimiter(perMinute float64, now func() time.Time) *ipRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &ipRateLimiter{
		buckets:  make(map[string]*ipBucket),
		capacity: perMinute,
		refill:   perMinute / 60.0,
		now:      now,
	}
}

// allow consumes one token for ip, returning false when the bucket is empty.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[ip]
	if !ok {
		// Opportunistic GC so the map can't grow unbounded: drop buckets that
		// have been idle long enough to have fully refilled.
		if len(l.buckets) > 4096 {
			for k, v := range l.buckets {
				if now.Sub(v.last) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		l.buckets[ip] = &ipBucket{tokens: l.capacity - 1, last: now}
		return true
	}
	// Refill based on elapsed time, cap at capacity.
	b.tokens += now.Sub(b.last).Seconds() * l.refill
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
