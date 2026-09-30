package auth

import (
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// ceremonyTTL bounds how long a passkey prompt may stay open.
const (
	ceremonyTTL = 5 * time.Minute
	ceremonyMax = 256 // pending ceremonies kept in memory
)

type ceremonyKind int

const (
	ceremonyLogin ceremonyKind = iota + 1
	ceremonyRegister
)

type ceremony struct {
	kind    ceremonyKind
	data    webauthn.SessionData
	name    string // passkey name (registration)
	session string // public session id that began a registration
	expires time.Time
}

// ceremonyStore keeps WebAuthn session data in memory, keyed by a random id
// sent to the browser in a short-lived cookie. Entries are single use.
type ceremonyStore struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*ceremony
}

func newCeremonyStore(now func() time.Time) *ceremonyStore {
	return &ceremonyStore{now: now, m: map[string]*ceremony{}}
}

// put stores c and returns its id.
func (s *ceremonyStore) put(c *ceremony) string {
	id := randomHex(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	c.expires = now.Add(ceremonyTTL)
	if len(s.m) >= ceremonyMax {
		s.gcLocked(now)
		if len(s.m) >= ceremonyMax { // still full: evict the oldest
			var oldID string
			var old time.Time
			for k, v := range s.m {
				if oldID == "" || v.expires.Before(old) {
					oldID, old = k, v.expires
				}
			}
			delete(s.m, oldID)
		}
	}
	s.m[id] = c
	return id
}

// take removes and returns the ceremony if it exists, has the expected kind
// and has not expired.
func (s *ceremonyStore) take(id string, kind ceremonyKind) *ceremony {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.m[id]
	if c == nil {
		return nil
	}
	delete(s.m, id)
	if c.kind != kind || !s.now().Before(c.expires) {
		return nil
	}
	return c
}

func (s *ceremonyStore) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(s.now())
}

func (s *ceremonyStore) gcLocked(now time.Time) {
	for k, v := range s.m {
		if !now.Before(v.expires) {
			delete(s.m, k)
		}
	}
}
