package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/server"
)

// SocketValid checks the original credential against SQLite without touching
// activity or sliding expiry. An idle socket cannot keep a session alive.
func (s *Service) SocketValid(ctx context.Context, r *http.Request, p *server.Principal) bool {
	has, err := s.acc.HasUser(ctx)
	if err != nil || !has {
		return false
	}
	switch p.Method {
	case "cookie":
		c, err := r.Cookie(s.cookieName(r, sessionCookie))
		if err != nil {
			return false
		}
		row, err := s.acc.sessionByHash(ctx, secret.HashToken(c.Value))
		return err == nil && row != nil && row.ID == p.SessionID
	case "token":
		scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return false
		}
		tok = strings.TrimSpace(tok)
		row, err := s.acc.lookupToken(ctx, secret.HashToken(tok))
		return err == nil && row != nil && row.ID == p.TokenID
	}
	return false
}

// SubscribeRevocations registers before the first socket validation. The bus
// can drop notifications; SocketGuard also checks SQLite every second.
func (s *Service) SubscribeRevocations(p *server.Principal) (<-chan api.Event, func()) {
	if s.d.Bus == nil {
		return nil, func() {}
	}
	sub := s.d.Bus.Subscribe(1, func(ev api.Event) bool {
		if ev.Type != core.BusSessionRevoked {
			return false
		}
		var v core.SessionRevoked
		switch data := ev.Data.(type) {
		case core.SessionRevoked:
			v = data
		case *core.SessionRevoked:
			if data != nil {
				v = *data
			}
		}
		for _, id := range v.SessionIDs {
			if p.Method == "cookie" && id == p.SessionID {
				return true
			}
		}
		for _, id := range v.TokenIDs {
			if p.Method == "token" && id == p.TokenID {
				return true
			}
		}
		return false
	})
	return sub.C, sub.Close
}
