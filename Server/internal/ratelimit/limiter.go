// Package ratelimit provides a per-key token bucket plus an in-flight cap.
//
// Sign submission limits are deliberately in-process: the deployment runs a
// single Server instance and the goal is to stop one account from hammering
// upstream, not to enforce a cluster-wide quota.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is safe for concurrent use. A nil *Limiter or one created with a
// non-positive perMinute and maxConcurrent allows everything.
type Limiter struct {
	mu            sync.Mutex
	perMinute     float64
	burst         float64
	maxConcurrent int
	now           func() time.Time
	buckets       map[string]*bucket
}

type bucket struct {
	tokens   float64
	last     time.Time
	inflight int
}

// Decision describes a rejected acquisition. RetryAfter is a hint for the
// Retry-After response header.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Reason     string
}

const (
	ReasonRate        = "rate"
	ReasonConcurrency = "concurrency"
)

func New(perMinute, burst, maxConcurrent int) *Limiter {
	if burst <= 0 {
		burst = perMinute
	}
	return &Limiter{
		perMinute:     float64(perMinute),
		burst:         float64(burst),
		maxConcurrent: maxConcurrent,
		now:           time.Now,
		buckets:       make(map[string]*bucket),
	}
}

// Acquire consumes one request token and one concurrency slot for key. The
// returned release function must be called exactly once when the submission
// finishes. A rejected Acquire returns a nil release.
func (l *Limiter) Acquire(key string) (func(), Decision) {
	if l == nil || (l.perMinute <= 0 && l.maxConcurrent <= 0) {
		return func() {}, Decision{Allowed: true}
	}
	if key == "" {
		key = "anonymous"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry := l.buckets[key]
	if entry == nil {
		entry = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = entry
		l.cleanupLocked(now)
	} else if l.perMinute > 0 {
		elapsed := now.Sub(entry.last).Seconds()
		if elapsed > 0 {
			entry.tokens += elapsed * l.perMinute / 60
			if entry.tokens > l.burst {
				entry.tokens = l.burst
			}
			entry.last = now
		}
	}

	if l.maxConcurrent > 0 && entry.inflight >= l.maxConcurrent {
		return nil, Decision{Allowed: false, RetryAfter: time.Second, Reason: ReasonConcurrency}
	}
	if l.perMinute > 0 && entry.tokens < 1 {
		wait := time.Duration((1 - entry.tokens) * 60 / l.perMinute * float64(time.Second))
		if wait < time.Second {
			wait = time.Second
		}
		return nil, Decision{Allowed: false, RetryAfter: wait, Reason: ReasonRate}
	}
	if l.perMinute > 0 {
		entry.tokens--
	}
	entry.inflight++

	return func() { l.release(key) }, Decision{Allowed: true}
}

func (l *Limiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.buckets[key]
	if entry == nil {
		return
	}
	if entry.inflight > 0 {
		entry.inflight--
	}
	if entry.inflight == 0 && (l.perMinute <= 0 || entry.tokens >= l.burst) {
		delete(l.buckets, key)
	}
}

// cleanupLocked bounds memory for one-off keys. Idle full buckets are
// equivalent to a missing bucket, so dropping them is lossless.
func (l *Limiter) cleanupLocked(now time.Time) {
	if len(l.buckets) <= 4096 {
		return
	}
	for key, entry := range l.buckets {
		if entry.inflight != 0 {
			continue
		}
		if l.perMinute > 0 {
			entry.tokens += now.Sub(entry.last).Seconds() * l.perMinute / 60
			if entry.tokens > l.burst {
				entry.tokens = l.burst
			}
		}
		if entry.tokens >= l.burst {
			delete(l.buckets, key)
		}
	}
}
