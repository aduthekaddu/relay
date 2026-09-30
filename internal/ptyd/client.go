package ptyd

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	// clientQueueMax bounds the live output queued for one client.
	clientQueueMax = 1 << 20
	// flowWindow is how far a client may run ahead of its acknowledgements
	// before the server stops writing to it.
	flowWindow = 1 << 20
	// writeTimeout bounds a single WebSocket write.
	writeTimeout = 30 * time.Second
)

type frame struct {
	text    bool
	data    []byte
	counted bool // live output counted against clientQueueMax
}

// client is one attached WebSocket. Output is queued by the session under
// its lock and written by the client's own writer goroutine, so a slow
// client never blocks the pty reader or the other clients.
type client struct {
	conn     *websocket.Conn
	readOnly bool

	mu      sync.Mutex
	q       []frame
	qBytes  int
	lagging bool
	closed  bool
	wake    chan struct{}
	ackWake chan struct{}

	sent  int64 // binary bytes written
	acked int64 // cumulative bytes acknowledged by the client
	flow  bool  // the client acknowledges, so flow control applies

	// Guarded by the session lock.
	cols, rows int
	lastActive time.Time
	visible    bool
}

func newClient(conn *websocket.Conn, readOnly bool, cols, rows int) *client {
	return &client{
		conn:     conn,
		readOnly: readOnly,
		wake:     make(chan struct{}, 1),
		ackWake:  make(chan struct{}, 1),
		cols:     cols,
		rows:     rows,
		visible:  true,
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// push queues f. It returns false when the client fell too far behind and
// must be dropped (the writer then sends "lagging" and closes).
func (c *client) push(f frame) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.lagging {
		return !c.lagging
	}
	if f.counted {
		if c.qBytes+len(f.data) > clientQueueMax {
			c.lagging = true
			c.q = nil
			c.qBytes = 0
			signal(c.wake)
			return false
		}
		c.qBytes += len(f.data)
	}
	c.q = append(c.q, f)
	signal(c.wake)
	return true
}

// pushMsg queues a JSON control message.
func (c *client) pushMsg(m api.TermServerMsg) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	c.push(frame{text: true, data: b})
}

// ack records a cumulative acknowledgement from the client.
func (c *client) ack(total int64) {
	c.mu.Lock()
	c.flow = true
	if total > c.acked {
		c.acked = total
	}
	c.mu.Unlock()
	signal(c.ackWake)
}

// finish stops the writer after it drained the queue.
func (c *client) finish() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	signal(c.wake)
}

// errLagging ends a writer whose client fell behind.
var errLagging = errors.New("lagging")

// writeLoop drains the queue until ctx is done, the client is finished
// (returns nil after flushing) or it lags (returns errLagging).
func (c *client) writeLoop(ctx context.Context) error {
	for {
		c.mu.Lock()
		q := c.q
		c.q = nil
		c.qBytes = 0
		lag, closed := c.lagging, c.closed
		c.mu.Unlock()

		if lag {
			msg, _ := json.Marshal(api.TermServerMsg{T: "error", Message: "lagging"})
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			return errLagging
		}
		for _, f := range q {
			if err := c.write(ctx, f); err != nil {
				return err
			}
		}
		if closed && len(q) == 0 {
			return nil
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.wake:
		}
	}
}

func (c *client) write(ctx context.Context, f frame) error {
	if !f.text {
		if err := c.waitWindow(ctx); err != nil {
			return err
		}
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	typ := websocket.MessageBinary
	if f.text {
		typ = websocket.MessageText
	}
	if err := c.conn.Write(wctx, typ, f.data); err != nil {
		return err
	}
	if !f.text {
		c.mu.Lock()
		c.sent += int64(len(f.data))
		c.mu.Unlock()
	}
	return nil
}

// waitWindow blocks while the client is more than flowWindow bytes behind
// its acknowledgements. Output keeps queueing meanwhile; if the queue
// overflows the client is dropped as lagging.
func (c *client) waitWindow(ctx context.Context) error {
	for {
		c.mu.Lock()
		behind := c.flow && c.sent-c.acked > flowWindow
		lag := c.lagging
		c.mu.Unlock()
		if !behind || lag {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ackWake:
		case <-c.wake:
			// re-check lagging
		}
	}
}
