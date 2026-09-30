package search

import (
	"sync"
	"time"
)

// bucket is a token-bucket rate limiter: rate tokens per second, up to
// burst stored.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newBucket(rate, burst float64, now func() time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now(), now: now}
}

// take consumes a token. When none is available it returns how long until
// one is.
func (b *bucket) take() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = min(b.burst, b.tokens+el*b.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	return time.Duration((1 - b.tokens) / b.rate * float64(time.Second)), false
}

// askLimiter allows one Quick AI run at a time and at most max runs per
// window (sliding).
type askLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	busy   bool
	starts []time.Time
	now    func() time.Time
}

func newAskLimiter(max int, window time.Duration, now func() time.Time) *askLimiter {
	return &askLimiter{max: max, window: window, now: now}
}

// limitErr describes a refusal: busy (a run is in flight) or quota spent,
// with a retry hint.
type limitErr struct {
	busy bool
	wait time.Duration
}

// acquire reserves the single run slot and records a start. The returned
// release must be called when the run ends.
func (l *askLimiter) acquire() (release func(), refused *limitErr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-l.window)
	keep := l.starts[:0]
	for _, t := range l.starts {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.starts = keep
	if l.busy {
		return nil, &limitErr{busy: true, wait: time.Second}
	}
	if len(l.starts) >= l.max {
		return nil, &limitErr{wait: l.starts[0].Add(l.window).Sub(now)}
	}
	l.busy = true
	l.starts = append(l.starts, now)
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.busy = false
			l.mu.Unlock()
		})
	}, nil
}
