package auth

import (
	"math"
	"sync"
	"time"
)

// limiter combines, per key (client IP):
//   - a token bucket of failures (burst attempts, refilled at rate), and
//   - an exponential lockout once the bucket is empty (base, doubling per
//     strike, capped at maxLock),
//
// plus a global failure bucket shared by every key, so a distributed guess
// run is capped too. Successful attempts never consume tokens and reset the
// key's strikes.
type limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	burst   float64
	rate    float64 // tokens per second
	base    time.Duration
	maxLock time.Duration
	keys    map[string]*bucket
	global  bucket
	gBurst  float64
	gRate   float64
	maxKeys int
}

type bucket struct {
	tokens  float64
	last    time.Time
	strikes int
	until   time.Time
}

// limiterConfig: burst failures per window, and a global cap.
type limiterConfig struct {
	Burst        int           // failures allowed before lockout (per key)
	Window       time.Duration // time to refill Burst tokens
	BaseLock     time.Duration // first lockout
	MaxLock      time.Duration // lockout cap
	GlobalBurst  int           // failures allowed across all keys…
	GlobalWindow time.Duration // …per this window
}

// defaultLimits: 5 failures / 5 min per IP, lockouts 1 min → 1 h, and at
// most 100 failures / 10 min machine-wide.
var defaultLimits = limiterConfig{
	Burst: 5, Window: 5 * time.Minute, BaseLock: time.Minute, MaxLock: time.Hour,
	GlobalBurst: 100, GlobalWindow: 10 * time.Minute,
}

func newLimiter(c limiterConfig, now func() time.Time) *limiter {
	l := &limiter{
		now: now, burst: float64(c.Burst), rate: float64(c.Burst) / c.Window.Seconds(),
		base: c.BaseLock, maxLock: c.MaxLock, keys: map[string]*bucket{},
		gBurst: float64(c.GlobalBurst), gRate: float64(c.GlobalBurst) / c.GlobalWindow.Seconds(),
		maxKeys: 10000,
	}
	l.global = bucket{tokens: l.gBurst, last: now()}
	return l
}

func (b *bucket) refill(now time.Time, burst, rate float64) {
	if b.last.IsZero() {
		b.tokens, b.last = burst, now
		return
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(burst, b.tokens+el*rate)
		b.last = now
	}
}

// wait returns how long until one token is available.
func (b *bucket) wait(rate float64) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// Allow reports whether key may attempt now, and otherwise how long to wait.
func (l *limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.global.refill(now, l.gBurst, l.gRate)
	if w := l.global.wait(l.gRate); w > 0 {
		return false, w
	}
	b := l.keys[key]
	if b == nil {
		return true, 0
	}
	if now.Before(b.until) {
		return false, b.until.Sub(now)
	}
	b.refill(now, l.burst, l.rate)
	if w := b.wait(l.rate); w > 0 {
		return false, w
	}
	return true, 0
}

// Fail records a failed attempt by key.
func (l *limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.global.refill(now, l.gBurst, l.gRate)
	l.global.tokens = math.Max(0, l.global.tokens-1)
	b := l.keys[key]
	if b == nil {
		if len(l.keys) >= l.maxKeys {
			l.gcLocked(now)
		}
		b = &bucket{}
		l.keys[key] = b
	}
	b.refill(now, l.burst, l.rate)
	b.tokens = math.Max(0, b.tokens-1)
	if b.tokens < 1 {
		b.strikes++
		lock := l.base << min(b.strikes-1, 20)
		if lock > l.maxLock || lock <= 0 {
			lock = l.maxLock
		}
		b.until = now.Add(lock)
	}
}

// Success clears the key's strikes (tokens refill on their own).
func (l *limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b := l.keys[key]; b != nil {
		b.strikes = 0
		b.until = time.Time{}
	}
}

// GC drops keys that are fully refilled and not locked.
func (l *limiter) GC() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked(l.now())
}

func (l *limiter) gcLocked(now time.Time) {
	for k, b := range l.keys {
		b.refill(now, l.burst, l.rate)
		if now.After(b.until) && b.tokens >= l.burst {
			delete(l.keys, k)
		}
	}
	// Under a flood of distinct keys, forget the oldest state rather than
	// growing without bound; the global bucket still caps attempts.
	if len(l.keys) >= l.maxKeys {
		for k := range l.keys {
			delete(l.keys, k)
			if len(l.keys) < l.maxKeys/2 {
				break
			}
		}
	}
}
