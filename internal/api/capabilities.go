package api

import "time"

// Capabilities are read-only snapshots from feature owners. Installation
// never establishes readiness; running requires the owner's ready processes.
type Capabilities struct {
	Previews PreviewCapability `json:"previews"`
	Code     AppCapability     `json:"code"`
	Desktop  AppCapability     `json:"desktop"`
}

// PreviewCapability is the decision used to generate preview URLs. Host
// excludes its port. An explicit subdomain setting verifies neither DNS nor TLS.
type PreviewCapability struct {
	ConfiguredMode string     `json:"configuredMode"`
	EffectiveMode  string     `json:"effectiveMode"`
	Host           string     `json:"host"`
	Port           string     `json:"port,omitempty"`
	Detection      string     `json:"detection"`
	CheckedAt      *time.Time `json:"checkedAt,omitempty"`
}

// AppCapability separates the enabled setting, fresh prerequisites and the
// lifecycle. Missing has stable identifiers, never paths or subprocess output.
// Implementation is the active selection while starting/running, otherwise
// the candidate for the next explicit start.
type AppCapability struct {
	Enabled        bool     `json:"enabled"`
	Available      bool     `json:"available"`
	State          string   `json:"state"` // disabled | unavailable | stopped | starting | running | failed
	Missing        []string `json:"missing"`
	Implementation string   `json:"implementation,omitempty"`
	Source         string   `json:"source,omitempty"` // configured | path | local-bin | standalone
}

// CapabilityChange invalidates cached Info without exposing environment data.
type CapabilityChange struct {
	Feature string `json:"feature"` // previews | code | desktop
}

const EvCapabilitiesChanged = "capabilities.changed"
