// Package search powers the command center: federated search, script commands and Quick AI.
//
// STUB: replaced by the owning feature. Keep the exported shape:
// New(*core.Deps) (*Service, error) and (*Service).Routes(*server.Router).
package search

import (
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
)

type Service struct{ d *core.Deps }

func New(d *core.Deps) (*Service, error) { return &Service{d: d}, nil }

func (s *Service) Routes(rt *server.Router) {}
