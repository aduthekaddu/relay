package info

import "github.com/aduthekaddu/relay/internal/api"

func (s *Service) capabilities() api.Capabilities {
	out := api.Capabilities{
		Previews: api.PreviewCapability{ConfiguredMode: s.d.Cfg.Previews.Mode, EffectiveMode: "off", Detection: "owner-unavailable"},
		Code:     api.AppCapability{Enabled: s.d.Cfg.Code.Enabled, State: "unavailable", Missing: []string{"owner"}},
		Desktop:  api.AppCapability{Enabled: s.d.Cfg.Desktop.Enabled, State: "unavailable", Missing: []string{"owner"}},
	}
	if !out.Code.Enabled {
		out.Code.State = "disabled"
	}
	if !out.Desktop.Enabled {
		out.Desktop.State = "disabled"
	}
	if s.d.Previews != nil {
		out.Previews = s.d.Previews.PreviewCapability()
	}
	if s.d.Apps != nil {
		out.Code = s.d.Apps.CodeCapability()
		out.Desktop = s.d.Apps.DesktopCapability()
	}
	return out
}
