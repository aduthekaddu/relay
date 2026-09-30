// Package events is the in-process publish/subscribe bus that feeds the
// browser's live event socket (/api/v1/events) and lets features react to
// each other without importing each other.
package events

import (
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Bus fans events out to subscribers. Publishing never blocks: a slow
// subscriber drops events rather than stalling the publisher.
type Bus struct {
	mu   sync.RWMutex
	subs map[*Sub]struct{}
}

type Sub struct {
	C      chan api.Event
	filter func(api.Event) bool
	bus    *Bus
	once   sync.Once
	// Dropped counts events lost because C was full.
	Dropped int
}

func New() *Bus { return &Bus{subs: map[*Sub]struct{}{}} }

// Subscribe returns a subscription with a buffered channel. filter may be
// nil (receive everything). Call Close when done.
func (b *Bus) Subscribe(buffer int, filter func(api.Event) bool) *Sub {
	if buffer <= 0 {
		buffer = 64
	}
	s := &Sub{C: make(chan api.Event, buffer), filter: filter, bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Close unsubscribes and closes C.
func (s *Sub) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
		close(s.C)
	})
}

// Publish sends an event of type t with data to all subscribers.
func (b *Bus) Publish(t string, data any) {
	ev := api.Event{Type: t, At: time.Now().UTC(), Data: data}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.filter != nil && !s.filter(ev) {
			continue
		}
		select {
		case s.C <- ev:
		default:
			s.Dropped++
		}
	}
}

// Subscribers returns the number of active subscriptions.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
