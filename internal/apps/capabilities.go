package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"

	"github.com/aduthekaddu/relay/internal/api"
)

func capabilityState(enabled, available bool, lifecycle string) string {
	if !enabled {
		return "disabled"
	}
	switch lifecycle {
	case stateStarting, stateRunning:
		return lifecycle
	case stateError:
		return "failed"
	}
	if !available {
		return "unavailable"
	}
	return stateStopped
}

func (s *Service) CodeCapability() api.AppCapability {
	_, flavor, source, ok := discoverCode(s.d.Cfg.Code.Binary, s.d.Paths.Home, s.lookPath)
	out := api.AppCapability{Enabled: s.d.Cfg.Code.Enabled, Available: ok, Missing: []string{}, Source: source}
	if !ok {
		out.Missing = append(out.Missing, "code-binary")
	}
	if ok {
		out.Implementation = codeImplementation(flavor)
	}
	lifecycle := stateStopped
	if a := s.apps["code"]; a != nil && a.proc != nil {
		lifecycle, _, _ = a.proc.Status()
		if lifecycle == stateRunning || (lifecycle == stateStarting && a.proc.PID() != 0) {
			a.capMu.Lock()
			if a.implementation != "" {
				out.Implementation, out.Source = a.implementation, a.source
			}
			a.capMu.Unlock()
		}
	}
	out.State = capabilityState(out.Enabled, out.Available, lifecycle)
	return out
}
func codeImplementation(flavor ideFlavor) string {
	if flavor == flavorOpenVSCode {
		return "openvscode-server"
	}
	return "code-server"
}
func (s *Service) DesktopCapability() api.AppCapability { return s.desk.Capability() }

// probePrerequisites inspects only prerequisites, not X sockets or processes.
// Occupied displays and failures while preparing/starting are lifecycle errors.
func (k *desktop) probePrerequisites() (api.AppCapability, string) {
	platform := k.platform
	if platform == "" {
		platform = runtime.GOOS
	}
	out := api.AppCapability{Enabled: k.d.Cfg.Desktop.Enabled, Missing: []string{}}
	if platform != "linux" {
		out.Missing = append(out.Missing, "linux")
	}
	if !displayRe.MatchString(k.display) {
		out.Missing = append(out.Missing, "display")
	}
	if len(k.sock) > 100 {
		out.Missing = append(out.Missing, "runtime-path-length")
	}
	if !safePathRe.MatchString(k.dir) {
		out.Missing = append(out.Missing, "runtime-path-characters")
	}
	if k.unavailable != "" {
		out.Missing = append(out.Missing, "configuration")
	}
	look := k.lookPath
	var bin, openbox string
	if look != nil {
		bin = findFirst(look, "Xvnc", "Xtigervnc")
		openbox = findFirst(look, "openbox")
	}
	if bin == "" {
		out.Missing = append(out.Missing, "vnc")
	}
	if openbox == "" {
		out.Missing = append(out.Missing, "openbox")
	}
	out.Available = len(out.Missing) == 0
	if bin != "" {
		out.Implementation = filepath.Base(bin)
		out.Source = "path"
	}
	return out, bin
}

func (k *desktop) lifecycle() string {
	xs, ss := stateStopped, stateStopped
	if k.xvnc != nil {
		xs, _, _ = k.xvnc.Status()
	}
	if k.session != nil {
		ss, _, _ = k.session.Status()
	}
	k.mu.Lock()
	failed := k.failed
	k.mu.Unlock()
	switch {
	case xs == stateError || ss == stateError || failed:
		return stateError
	case xs == stateRunning && ss == stateRunning:
		return stateRunning
	case xs == stateStarting || xs == stateRunning || ss == stateStarting:
		return stateStarting
	default:
		return stateStopped
	}
}
func (k *desktop) Capability() api.AppCapability {
	out, _ := k.probePrerequisites()
	lifecycle := k.lifecycle()
	out.State = capabilityState(out.Enabled, out.Available, lifecycle)
	if lifecycle == stateRunning || (lifecycle == stateStarting && k.xvnc != nil && k.xvnc.PID() != 0) {
		k.mu.Lock()
		if k.implementation != "" {
			out.Implementation = k.implementation
		}
		k.mu.Unlock()
	}
	return out
}

func (s *Service) codeState(a *webApp) api.App {
	cap := s.CodeCapability()
	out := api.App{ID: a.id, Name: a.name, Description: a.desc, Kind: a.kind, Icon: a.icon, URL: a.base + "/", Installed: cap.Available, Capability: &cap}
	out.State = legacyAppState(cap.State)
	if cap.State == "unavailable" {
		out.InstallHint = a.installHint
	}
	if a.proc != nil {
		_, since, _ := a.proc.Status()
		if cap.State == "starting" || cap.State == "running" || cap.State == "failed" {
			out.Since = since
		}
	}
	if cap.State == "failed" {
		out.Error = "Code could not start or exited unexpectedly."
	}
	return out
}
func legacyAppState(state string) string {
	switch state {
	case "disabled":
		return "unavailable"
	case "failed":
		return stateError
	default:
		return state
	}
}
func (s *Service) appUsable(a *webApp) bool {
	if a.id != "code" {
		return a.installed
	}
	cap := s.CodeCapability()
	return cap.Enabled && (cap.Available || cap.State == stateStarting || cap.State == stateRunning)
}

// refreshCapabilities invalidates browser Info after discovery/lifecycle
// changes. Queries are always fresh; the idle loop polls on its 30s timer; stop work can delay a poll.
func (s *Service) refreshCapabilities() {
	code, desk := s.CodeCapability(), s.DesktopCapability()
	s.capMu.Lock()
	cc, dc := !reflect.DeepEqual(code, s.lastCode), !reflect.DeepEqual(desk, s.lastDesk)
	s.lastCode, s.lastDesk = code, desk
	s.capMu.Unlock()
	if s.d.Bus != nil {
		if cc {
			s.d.Bus.Publish(api.EvCapabilitiesChanged, api.CapabilityChange{Feature: "code"})
		}
		if dc {
			s.d.Bus.Publish(api.EvCapabilitiesChanged, api.CapabilityChange{Feature: "desktop"})
		}
	}
}

func (s *Service) resolveCode(a *webApp, sock string) ([]string, []string, error) {
	bin, flavor, source, ok := discoverCode(s.d.Cfg.Code.Binary, s.d.Paths.Home, s.lookPath)
	if !s.d.Cfg.Code.Enabled || !ok {
		return nil, nil, fmt.Errorf("Code binary is unavailable")
	}
	a.capMu.Lock()
	a.implementation, a.source = codeImplementation(flavor), source
	a.capMu.Unlock()
	return codeArgs(bin, flavor, sock, s.d.Paths.DataDir, a.base), childEnv(os.Environ(), nil), nil
}

// resolveXvnc revalidates the tools for each explicit start.
func (k *desktop) resolveXvnc() ([]string, []string, error) {
	cap, bin := k.probePrerequisites()
	if !cap.Enabled || !cap.Available {
		return nil, nil, fmt.Errorf("desktop prerequisites are unavailable")
	}
	k.mu.Lock()
	k.implementation = filepath.Base(bin)
	k.mu.Unlock()
	return k.xvncArgs(bin), k.sessionEnv(), nil
}
func (k *desktop) resolveSession() ([]string, []string, error) {
	cap, _ := k.probePrerequisites()
	if !cap.Enabled || !cap.Available {
		return nil, nil, fmt.Errorf("desktop prerequisites are unavailable")
	}
	argv := []string{"/bin/sh", filepath.Join(k.dir, "xstartup")}
	if drs := findFirst(k.lookPath, "dbus-run-session"); drs != "" {
		argv = append([]string{drs, "--"}, argv...)
	}
	return argv, k.sessionEnv(), nil
}
