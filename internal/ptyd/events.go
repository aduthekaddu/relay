package ptyd

import (
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// subscriberBuffer bounds the events queued for one /v1/events client. A
// subscriber that falls this far behind is disconnected; it reconnects
// and re-lists, which is cheaper than stalling every session.
const subscriberBuffer = 512

type subscriber struct {
	ch     chan ptyclient.PtyEvent
	closed bool // guarded by Daemon.subsMu
}

func (d *Daemon) subscribe() *subscriber {
	s := &subscriber{ch: make(chan ptyclient.PtyEvent, subscriberBuffer)}
	d.subsMu.Lock()
	d.subs[s] = struct{}{}
	d.subsMu.Unlock()
	return s
}

func (d *Daemon) unsubscribe(s *subscriber) {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	if !s.closed {
		s.closed = true
		delete(d.subs, s)
		close(s.ch)
	}
}

func (d *Daemon) closeSubscribers() {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for s := range d.subs {
		s.closed = true
		close(s.ch)
	}
	clear(d.subs)
}

// emit fans events out without blocking. Callers must not hold a
// session lock.
func (d *Daemon) emit(evs ...ptyclient.PtyEvent) {
	if len(evs) == 0 {
		return
	}
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for s := range d.subs {
		for _, ev := range evs {
			select {
			case s.ch <- ev:
			default:
				d.log.Warn("dropping slow event subscriber")
				s.closed = true
				delete(d.subs, s)
				close(s.ch)
			}
			if s.closed {
				break
			}
		}
	}
}
