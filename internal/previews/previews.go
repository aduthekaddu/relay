// Package previews detects listening dev servers and proxies them as authenticated previews.
//
// STUB: replaced by the owning feature. Keep the exported shape:
// New(*core.Deps) (*Service, error) and (*Service).Routes(*server.Router).
package previews

import (
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
)

type Service struct{ d *core.Deps }

func New(d *core.Deps) (*Service, error) { return &Service{d: d}, nil }

func (s *Service) Routes(rt *server.Router) {}
