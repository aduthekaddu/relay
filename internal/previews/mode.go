package previews

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Modes.
const (
	modeAuto      = "auto"
	modeSubdomain = "subdomain"
	modePath      = "path"
	modeOff       = "off"
)

// origin is the parsed external Relay origin.
type origin struct {
	Scheme string // http | https
	Host   string // hostname without port
	Port   string // explicit port or ""
}

func parseOrigin(s string) origin {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return origin{Scheme: "http", Host: "localhost"}
	}
	return origin{Scheme: u.Scheme, Host: strings.ToLower(u.Hostname()), Port: u.Port()}
}

func (o origin) String() string {
	h := o.Host
	if o.Port != "" {
		h = net.JoinHostPort(o.Host, o.Port)
	}
	return o.Scheme + "://" + h
}

// Secure reports whether the origin is HTTPS.
func (o origin) Secure() bool { return o.Scheme == "https" }

// previewHost is the host (with the origin's explicit port, if any) that
// serves port in subdomain mode.
func previewHost(port int, base string, o origin) string {
	h := strconv.Itoa(port) + "." + base
	if o.Port != "" {
		h = net.JoinHostPort(h, o.Port)
	}
	return h
}

// subdomainOrigin is the browser origin of a subdomain preview.
func subdomainOrigin(port int, base string, o origin) string {
	return o.Scheme + "://" + previewHost(port, base, o)
}

// previewURL returns the URL a browser should open for port.
func previewURL(mode string, port int, base string, o origin) string {
	switch mode {
	case modeSubdomain:
		return subdomainOrigin(port, base, o) + "/"
	case modePath:
		return o.String() + "/p/" + strconv.Itoa(port) + "/"
	}
	return ""
}

// parsePreviewHost extracts the port from "<port>.<base>[:p]". ok is
// false for any other host.
func parsePreviewHost(host, base string) (int, bool) {
	if base == "" {
		return 0, false
	}
	h := strings.ToLower(host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(h, ".")
	suffix := "." + strings.ToLower(base)
	if !strings.HasSuffix(h, suffix) {
		return 0, false
	}
	label := strings.TrimSuffix(h, suffix)
	if label == "" || len(label) > 5 || strings.Contains(label, ".") {
		return 0, false
	}
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			return 0, false
		}
	}
	if label[0] == '0' {
		return 0, false // no "05173" aliases
	}
	port, err := strconv.Atoi(label)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// resolver is the subset of net.Resolver used for mode detection.
type resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// detectMode resolves "auto": subdomain when a random <n>.<host> resolves
// to the same addresses as <host> (wildcard DNS), else path. Loopback
// development hosts (localhost, *.localhost) always support subdomains.
func detectMode(ctx context.Context, configured, base string, r resolver) string {
	switch configured {
	case modeSubdomain, modePath, modeOff:
		if configured == modeSubdomain && base == "" {
			return modePath
		}
		return configured
	}
	if base == "" {
		return modePath
	}
	lb := strings.ToLower(base)
	if lb == "localhost" || strings.HasSuffix(lb, ".localhost") {
		return modeSubdomain
	}
	if net.ParseIP(strings.Trim(lb, "[]")) != nil {
		return modePath // IP literals cannot have subdomains
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	want, err := r.LookupHost(ctx, base)
	if err != nil || len(want) == 0 {
		return modePath
	}
	nb := make([]byte, 6)
	_, _ = rand.Read(nb)
	probe := "relay-" + hex.EncodeToString(nb) + "." + base
	got, err := r.LookupHost(ctx, probe)
	if err != nil || !sameAddrs(want, got) {
		return modePath
	}
	return modeSubdomain
}

func sameAddrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
