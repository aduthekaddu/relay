package apps

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
)

// Opt-in, read-only proof. It reads installation/configuration metadata from
// the named environment, while all mutable paths belong to the test fixture.
// It never starts Code, Xvnc, a desktop session, or a display probe.
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
	cfg.Apps = nil // This proof covers the two managed capabilities only.
	d := testDeps(t, nil)
	d.Cfg, d.Paths.Home = cfg, home
	s, err := New(d)
	if err != nil {
		t.Fatal("could not construct capability owner")
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error("owner cleanup failed")
		}
	})
	code, desktop := s.CodeCapability(), s.DesktopCapability()
	if code.State == "running" || code.State == "starting" || desktop.State == "running" || desktop.State == "starting" {
		t.Fatal("query started an optional service")
	}
	if s.desk.xvnc.PID() != 0 || s.desk.session.PID() != 0 {
		t.Fatal("query started a desktop process")
	}
	if a := s.apps["code"]; a != nil && a.proc.PID() != 0 {
		t.Fatal("query started Code")
	}
	_, statErr := os.Stat(file)
	report := struct {
		ConfigurationFilePresent bool `json:"configurationFilePresent"`
		Code, Desktop            any
		OptionalProcessesStarted bool `json:"optionalProcessesStarted"`
	}{ConfigurationFilePresent: statErr == nil, Code: code, Desktop: desktop}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("REL134_ENV " + string(b))
}
