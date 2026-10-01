package core

import "github.com/aduthekaddu/relay/internal/api"

// PreviewCapabilities is implemented by the preview URL owner.
type PreviewCapabilities interface {
	PreviewCapability() api.PreviewCapability
}

// AppCapabilities is implemented by the optional process owner. Queries
// inspect prerequisites and state only; they never start a service.
type AppCapabilities interface {
	CodeCapability() api.AppCapability
	DesktopCapability() api.AppCapability
}
