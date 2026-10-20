package ratelimit

import (
	"testing"
	"time"
)

func TestTokenBucketRefillsAndRejects(t *testing.T) {
	now := time.Unix(1700000000, 0)
	limiter := New(2, 2, 0)
	limiter.now = func() time.Time { return now }

	release1, decision := limiter.Acquire("u:1")
	if !decision.Allowed {
		t.Fatalf("first request rejected: %+v", decision)
	}
	release1()
	release2, decision := limiter.Acquire("u:1")
	if !decision.Allowed {
		t.Fatalf("second request rejected: %+v", decision)
	}
	release2()
	if _, decision := limiter.Acquire("u:1"); decision.Allowed || decision.Reason != ReasonRate {
		t.Fatalf("third request should hit the rate limit: %+v", decision)
	}

	now = now.Add(30 * time.Second) // one of two tokens per minute
	if _, decision := limiter.Acquire("u:1"); !decision.Allowed {
		t.Fatalf("token not refilled after 30s: %+v", decision)
	}
	if _, decision := limiter.Acquire("u:2"); !decision.Allowed {
		t.Fatalf("other accounts must be isolated: %+v", decision)
	}
}

func TestConcurrencyCapReleasesSlot(t *testing.T) {
	limiter := New(100, 100, 1)
	release, decision := limiter.Acquire("u:1")
	if !decision.Allowed {
		t.Fatalf("first request rejected: %+v", decision)
	}
	if _, decision := limiter.Acquire("u:1"); decision.Allowed || decision.Reason != ReasonConcurrency {
		t.Fatalf("concurrent request should be rejected: %+v", decision)
	}
	release()
	if release, decision := limiter.Acquire("u:1"); !decision.Allowed {
		t.Fatalf("slot not released: %+v", decision)
	} else {
		release()
	}
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	var limiter *Limiter
	release, decision := limiter.Acquire("u:1")
	if !decision.Allowed {
		t.Fatalf("nil limiter rejected: %+v", decision)
	}
	release()
}
