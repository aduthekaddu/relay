package agents

import (
	"context"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// ptyAPI is the subset of the ptyd client the agents feature uses. It is
// an interface so tests can run against an in-memory fake.
type ptyAPI interface {
	List(ctx context.Context) ([]api.TerminalSession, error)
	Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error)
	SetAttention(ctx context.Context, id string, a *api.Attention) error
}

// limiter is a small token bucket: n requests per period, refilled
// continuously. It protects expensive endpoints (search) from abuse.
type limiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	rate   float64 // tokens per second
	last   time.Time
	now    func() time.Time
}

func newLimiter(n int, per time.Duration) *limiter {
	return &limiter{tokens: float64(n), max: float64(n), rate: float64(n) / per.Seconds(), now: time.Now}
}

// allow takes one token, reporting false when the bucket is empty.
func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens = min(l.max, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
