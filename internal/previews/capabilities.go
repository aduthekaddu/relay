package previews

import (
	"context"

	"github.com/aduthekaddu/relay/internal/api"
)

func (s *Service) initialCapability() api.PreviewCapability {
	host, port, valid := normalizeHost(s.d.Cfg.Previews.Host)
	mode, detection := staticMode(configuredMode(s.d.Cfg.Previews.Mode), host, valid)
	if port == "" {
		port = s.origin.Port
	}
	return api.PreviewCapability{ConfiguredMode: configuredMode(s.d.Cfg.Previews.Mode), EffectiveMode: mode, Host: host, Port: port, Detection: detection}
}

// PreviewCapability is a defensive snapshot of the decision used by URL,
// host dispatch and TLS-host policy. Queries do not perform DNS lookups.
func (s *Service) PreviewCapability() api.PreviewCapability {
	out, ok := s.mode.Load().(api.PreviewCapability)
	if !ok {
		return s.initialCapability()
	}
	if out.CheckedAt != nil {
		v := *out.CheckedAt
		out.CheckedAt = &v
	}
	return out
}
func (s *Service) Mode() string     { return s.PreviewCapability().EffectiveMode }
func (s *Service) baseHost() string { return s.PreviewCapability().Host }

func (s *Service) subdomainOriginConfig() origin {
	out := s.origin
	out.Port = s.PreviewCapability().Port
	return out
}
func (s *Service) urlFor(mode string, port int) string {
	o := s.origin
	if mode == modeSubdomain {
		o = s.subdomainOriginConfig()
	}
	return previewURL(mode, port, s.baseHost(), o)
}

func (s *Service) redetectMode(ctx context.Context) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	next := s.initialCapability()
	mode, detection := detectModeResult(ctx, next.ConfiguredMode, s.d.Cfg.Previews.Host, s.res)
	if ctx.Err() != nil {
		return
	} // shutdown/caller cancellation does not revoke a prior decision
	next.EffectiveMode, next.Detection = mode, detection
	if next.Detection != "off" && next.Detection != "not-required" && next.Detection != "explicit" && next.Detection != "localhost" && next.Detection != "missing-host" && next.Detection != "invalid-host" && next.Detection != "ip-literal" {
		now := s.now().UTC()
		next.CheckedAt = &now
	}
	s.mu.Lock()
	old := s.PreviewCapability()
	s.mode.Store(next)
	changed := old.EffectiveMode != next.EffectiveMode
	var list []api.Preview
	if changed {
		list = s.listLocked()
		s.lastList = list
	}
	s.mu.Unlock()
	if s.d.Bus != nil {
		if changed {
			s.d.Bus.Publish(api.EvPreviewsChanged, list)
		}
		if changed || old.Detection != next.Detection {
			s.d.Bus.Publish(api.EvCapabilitiesChanged, api.CapabilityChange{Feature: "previews"})
		}
	}
}
