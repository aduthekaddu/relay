package previews

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
)

// No scans, URL requests, TLS changes or external fixture domains. Only an
// auto-mode hostname actually configured in the environment can invoke DNS.
func TestCapabilityEnvironment(t *testing.T) {
	if os.Getenv("REL134_ENV_PROBE") != "1" {
		t.Skip("opt-in read-only environment proof")
	}
	home, file := os.Getenv("REL134_ENV_HOME"), os.Getenv("REL134_ENV_CONFIG")
	if home == "" || file == "" {
		t.Fatal("environment metadata was not supplied")
	}
	cfg, err := config.Load(config.Paths{Home: home, ConfigFile: file})
	if err != nil {
		t.Fatal("could not read environment configuration")
	}
	h := newHarness(t, cfg.Previews.Mode, cfg.Previews.Host, cfg.Origin())
	initial := h.svc.PreviewCapability()
	h.svc.res = net.DefaultResolver
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	h.svc.redetectMode(ctx)
	cap := h.svc.PreviewCapability()
	if ctx.Err() != nil {
		t.Fatal("bounded environment query did not finish")
	}
	b, err := json.Marshal(struct {
		ConfiguredMode string `json:"configuredMode"`
		EffectiveMode  string `json:"effectiveMode"`
		Detection      string `json:"detection"`
		HostConfigured bool   `json:"hostConfigured"`
		DNSPerformed   bool   `json:"dnsPerformed"`
	}{cap.ConfiguredMode, cap.EffectiveMode, cap.Detection, cap.Host != "", initial.Detection == "pending"})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("REL134_ENV " + string(b))
}
