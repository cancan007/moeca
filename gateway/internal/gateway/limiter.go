package gateway

import (
	"sync"
	"time"
)

// tokenBucket is a simple, dependency-free rate limiter. rps <= 0 means allow.
type tokenBucket struct {
	mu       sync.Mutex
	rps      float64
	burst    float64
	tokens   float64
	last     time.Time
	nowFn    func() time.Time
}

func newTokenBucket(rps float64, burst int, nowFn func() time.Time) *tokenBucket {
	if nowFn == nil {
		nowFn = time.Now
	}
	b := float64(burst)
	if b < 1 {
		b = 1
	}
	return &tokenBucket{rps: rps, burst: b, tokens: b, last: nowFn(), nowFn: nowFn}
}

// allow consumes one token, refilling based on elapsed time. Returns false when
// the caller is over its sustained rate.
func (t *tokenBucket) allow() bool {
	if t.rps <= 0 {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.nowFn()
	elapsed := now.Sub(t.last).Seconds()
	t.last = now
	t.tokens += elapsed * t.rps
	if t.tokens > t.burst {
		t.tokens = t.burst
	}
	if t.tokens >= 1 {
		t.tokens--
		return true
	}
	return false
}

// limiterSet lazily creates one bucket per (session, service) key.
type limiterSet struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	nowFn    func() time.Time
}

func newLimiterSet(nowFn func() time.Time) *limiterSet {
	return &limiterSet{buckets: map[string]*tokenBucket{}, nowFn: nowFn}
}

func (l *limiterSet) allow(key string, rps float64, burst int) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = newTokenBucket(rps, burst, l.nowFn)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.allow()
}
