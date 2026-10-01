package info

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
)

type capabilityOwner struct {
	preview       api.PreviewCapability
	code, desktop api.AppCapability
}

func (o *capabilityOwner) PreviewCapability() api.PreviewCapability { return o.preview }
func (o *capabilityOwner) CodeCapability() api.AppCapability        { return o.code }
func (o *capabilityOwner) DesktopCapability() api.AppCapability     { return o.desktop }

func TestInfoUsesFreshOwnersAndPreservesStates(t *testing.T) {
	f := newFixture(t)
	o := &capabilityOwner{preview: api.PreviewCapability{ConfiguredMode: "auto", EffectiveMode: "path", Host: "dev.example.test", Detection: "pending"}}
	f.d.Previews, f.d.Apps = o, o
	for _, tc := range []struct {
		state              string
		enabled, available bool
	}{
		{"disabled", false, true}, {"unavailable", true, false}, {"stopped", true, true},
		{"starting", true, true}, {"running", true, true}, {"failed", true, true}, {"running", true, false},
	} {
		o.code = api.AppCapability{Enabled: tc.enabled, Available: tc.available, State: tc.state, Missing: []string{}}
		o.desktop = o.code
		w := f.do(t, "GET", "/api/v1/info", "", true)
		var got api.Info
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal("Info API failed")
		}
		if !reflect.DeepEqual(got.Capabilities.Code, o.code) || !reflect.DeepEqual(got.Capabilities.Desktop, o.desktop) {
			t.Fatalf("Info changed the owner's lifecycle %s", tc.state)
		}
		if got.Features.Code != (tc.enabled && tc.available) || got.Features.Desktop != (tc.enabled && tc.available) {
			t.Fatal("Legacy flags imply runtime readiness")
		}
	}
	for _, mode := range []string{"subdomain", "path", "subdomain"} {
		o.preview.EffectiveMode = mode
		o.preview.Detection = "verified"
		w := f.do(t, "GET", "/api/v1/info", "", true)
		var got api.Info
		if json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal("Info decode failed")
		}
		if !reflect.DeepEqual(got.Capabilities.Previews, o.preview) || got.Features.PreviewsMode != mode {
			t.Fatal("Info cached an old preview decision")
		}
		if (got.Features.PreviewsHost != "") != (mode == "subdomain") {
			t.Fatal("Legacy host meaning changed")
		}
	}
}
func TestInfoMissingOwnersDoesNotInferInstalledOrRunning(t *testing.T) {
	f := newFixture(t)
	f.d.Cfg.Code.Enabled = true
	f.d.Cfg.Desktop.Enabled = false
	got := f.svc.capabilities()
	if got.Code.State != "unavailable" || got.Code.Available || got.Desktop.State != "disabled" || got.Desktop.Available ||
		got.Previews.EffectiveMode != "off" || got.Previews.Detection != "owner-unavailable" {
		t.Fatalf("invented capability: %+v", got)
	}
}
