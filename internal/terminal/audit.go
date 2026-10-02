package terminal

import (
	"net"
	"net/http"

	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
)

// audit is the sole producer for terminal and upload mutations. Call it only
// after the mutation owner confirms success. Detail is a validated public ID
// and optional whitelisted signal;
// never pass names, commands, paths, request bodies or credential values.
func (s *Service) audit(r *http.Request, action, detail string) {
	if s.d.Bus == nil {
		return
	}
	ip := server.ClientIP(r)
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	} else if ip != "local" {
		// Proxy headers are request input, not necessarily an IP address.
		ip = ""
	}
	e := core.AuditEvent{Event: action, Detail: detail, IP: ip}
	if p := server.PrincipalFrom(r.Context()); p != nil {
		e.Actor = p.User
	}
	s.d.Bus.Publish(core.BusAudit, e)
}
