package api

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is a sliding-window per-key limiter intended for tracking
// failed login attempts per IP. Counts only what callers explicitly record,
// so successful operations don't burn quota.
type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:    limit,
		window:   window,
		attempts: make(map[string][]time.Time),
	}
}

// CountFailures returns how many failed attempts for key fall within the
// current window. Expired entries are evicted as a side effect.
func (r *RateLimiter) CountFailures(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.freshLocked(key, time.Now()))
}

// RecordFailure appends a failure timestamp for key. Callers should invoke
// this only when authentication actually fails.
func (r *RateLimiter) RecordFailure(key string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	fresh := r.freshLocked(key, now)
	r.attempts[key] = append(fresh, now)
}

// RetryAfter returns how long until the oldest in-window failure expires
// (i.e. when the caller can try again). Returns 0 if currently under the limit.
func (r *RateLimiter) RetryAfter(key string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	fresh := r.freshLocked(key, now)
	if len(fresh) < r.limit {
		return 0
	}
	return fresh[0].Add(r.window).Sub(now)
}

// Limit returns the configured failure limit.
func (r *RateLimiter) Limit() int { return r.limit }

// Cleanup removes empty entries and entries with no fresh attempts.
// Returns the number of keys evicted.
func (r *RateLimiter) Cleanup() int {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := 0
	for k := range r.attempts {
		fresh := r.freshLocked(k, now)
		if len(fresh) == 0 {
			delete(r.attempts, k)
			evicted++
		} else {
			r.attempts[k] = fresh
		}
	}
	return evicted
}

// StartJanitor runs Cleanup on a ticker until ctx is cancelled.
func (r *RateLimiter) StartJanitor(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.Cleanup()
			}
		}
	}()
}

// freshLocked returns the slice of attempts for key that fall within the window.
// Mutates r.attempts[key] to discard stale timestamps. Caller must hold r.mu.
func (r *RateLimiter) freshLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-r.window)
	list := r.attempts[key]
	fresh := list[:0]
	for _, t := range list {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) == 0 {
		delete(r.attempts, key)
	} else {
		r.attempts[key] = fresh
	}
	return fresh
}
