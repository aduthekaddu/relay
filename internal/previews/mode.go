package previews

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	modeAuto      = "auto"
	modeSubdomain = "subdomain"
	modePath      = "path"
	modeOff       = "off"
	dnsTimeout    = 3 * time.Second
)

type origin struct {
	Scheme string
	Host   string
	Port   string
}

// normalizeHost accepts a DNS name or IP literal, with an optional serving
// port. It rejects URL syntax so host input cannot inject a URL component.
func normalizeHost(raw string) (host, port string, valid bool) {
	h := strings.TrimSpace(raw)
	if h == "" {
		return "", "", true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.String(), "", true
	}
	if p, ps, err := net.SplitHostPort(h); err == nil {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return "", "", false
		}
		h, port = p, strconv.Itoa(n)
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = strings.Trim(h, "[]")
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if ip := net.ParseIP(h); ip != nil {
		return ip.String(), port, true
	}
	if h == "" || len(h) > 253 {
		return "", "", false
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", "", false
			}
		}
	}
	return h, port, true
}

func parseOrigin(raw string) origin {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return origin{Scheme: "http", Host: "localhost"}
	}
	h, p, ok := normalizeHost(u.Host)
	if !ok || (u.Scheme != "http" && u.Scheme != "https") {
		return origin{Scheme: "http", Host: "localhost"}
	}
	return origin{Scheme: u.Scheme, Host: h, Port: p}
}

func (o origin) String() string {
	h := o.Host
	if o.Port != "" {
		h = net.JoinHostPort(h, o.Port)
	} else if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return o.Scheme + "://" + h
}
func (o origin) Secure() bool { return o.Scheme == "https" }
func previewHost(port int, base string, o origin) string {
	h := strconv.Itoa(port) + "." + base
	if o.Port != "" {
		h = net.JoinHostPort(h, o.Port)
	}
	return h
}
func subdomainOrigin(port int, base string, o origin) string {
	return o.Scheme + "://" + previewHost(port, base, o)
}
func previewURL(mode string, port int, base string, o origin) string {
	switch mode {
	case modeSubdomain:
		return subdomainOrigin(port, base, o) + "/"
	case modePath:
		return o.String() + "/p/" + strconv.Itoa(port) + "/"
	}
	return ""
}
func parsePreviewHost(host, base string) (int, bool) {
	h, _, ok := normalizeHost(host)
	b, _, bok := normalizeHost(base)
	if !ok || !bok || b == "" {
		return 0, false
	}
	suffix := "." + b
	if !strings.HasSuffix(h, suffix) {
		return 0, false
	}
	label := strings.TrimSuffix(h, suffix)
	if label == "" || len(label) > 5 || label[0] == '0' {
		return 0, false
	}
	for _, c := range label {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	port, err := strconv.Atoi(label)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

type resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

func configuredMode(raw string) string {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case modeOff, modePath, modeSubdomain:
		return m
	default:
		return modeAuto
	}
}

// staticMode is also the safe startup decision before any DNS work. Explicit
// subdomain mode remains an operator choice, except for absent/invalid/IP hosts.
func staticMode(configured, base string, valid bool) (mode, detection string) {
	switch configured {
	case modeOff:
		return modeOff, "off"
	case modePath:
		return modePath, "not-required"
	}
	if !valid {
		return modePath, "invalid-host"
	}
	if base == "" {
		return modePath, "missing-host"
	}
	if net.ParseIP(base) != nil {
		return modePath, "ip-literal"
	}
	if configured == modeSubdomain {
		return modeSubdomain, "explicit"
	}
	if base == "localhost" || strings.HasSuffix(base, ".localhost") {
		return modeSubdomain, "localhost"
	}
	return modePath, "pending"
}

func detectModeResult(ctx context.Context, configured, raw string, r resolver) (mode, detection string) {
	base, _, valid := normalizeHost(raw)
	mode, detection = staticMode(configuredMode(configured), base, valid)
	if detection != "pending" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	if r == nil {
		return modePath, "lookup-failed"
	}
	want, err := r.LookupHost(ctx, base)
	if err != nil || len(want) == 0 {
		return modePath, lookupFailure(ctx, err)
	}
	nb := make([]byte, 6)
	if _, err = rand.Read(nb); err != nil {
		return modePath, "lookup-failed"
	}
	got, err := r.LookupHost(ctx, "relay-"+hex.EncodeToString(nb)+"."+base)
	if err != nil || len(got) == 0 {
		return modePath, lookupFailure(ctx, err)
	}
	if !sameAddrs(want, got) {
		return modePath, "address-mismatch"
	}
	return modeSubdomain, "verified"
}
func lookupFailure(ctx context.Context, err error) string {
	var dns *net.DNSError
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &dns) && dns.IsTimeout) {
		return "timeout"
	}
	return "lookup-failed"
}
func detectMode(ctx context.Context, configured, base string, r resolver) string {
	mode, _ := detectModeResult(ctx, configured, base, r)
	return mode
}
func sameAddrs(a, b []string) bool {
	normalize := func(in []string) []string {
		set := map[string]bool{}
		for _, s := range in {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil
			}
			set[ip.String()] = true
		}
		out := make([]string, 0, len(set))
		for ip := range set {
			out = append(out, ip)
		}
		sort.Strings(out)
		return out
	}
	a, b = normalize(a), normalize(b)
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
