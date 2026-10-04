package server

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// NormalizeOrigin validates an HTTP(S) origin and returns its browser form.
// It rejects URL decorations, ambiguous hosts, wildcards and remote HTTP IPs.
// Explicit default ports compare equal to omitted ports. No DNS is performed.
func NormalizeOrigin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.ContainsAny(raw, "?#\\% \t\r\n") {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || ip.Is4In6() || (scheme == "http" && !ip.IsLoopback()) {
			return "", false
		}
		// IPv6 must be bracketed; IPv4 must not be bracketed.
		if strings.HasPrefix(u.Host, "[") != ip.Is6() {
			return "", false
		}
		host = ip.String()
	} else if !originDomain(host) || strings.ContainsAny(u.Host, "[]") {
		return "", false
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
		if scheme == "http" && n == 80 || scheme == "https" && n == 443 {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, true
}

func originDomain(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	last := host[strings.LastIndexByte(host, '.')+1:]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return false
	}
	// Numeric host spellings such as 127.1 or 2130706433 are interpreted
	// differently by URL implementations. Require standard IP literals.
	numeric := true
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c >= 'a' && c <= 'z' || c == '-' {
				numeric = false
			} else if c < '0' || c > '9' {
				return false
			}
		}
	}
	return !numeric
}

// BrowserOrigin rejects absent, empty, duplicate and malformed Origin headers.
func BrowserOrigin(r *http.Request) (string, bool) {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return "", false
	}
	return NormalizeOrigin(values[0])
}

// OriginInList compares validated origins, accepting a configuration's single
// trailing slash for compatibility. Browser headers themselves cannot have one.
func OriginInList(origin string, allowed []string) bool {
	norm, ok := NormalizeOrigin(origin)
	if !ok {
		return false
	}
	for _, raw := range allowed {
		if candidate, ok := NormalizeOrigin(strings.TrimSuffix(raw, "/")); ok && candidate == norm {
			return true
		}
	}
	return false
}

// LocalOriginAliases adds only the three conventional HTTP loopback names at
// the canonical port. HTTPS certificates and other names get no implicit alias.
func LocalOriginAliases(canonical string) []string {
	norm, ok := NormalizeOrigin(canonical)
	if !ok {
		return nil
	}
	u, _ := url.Parse(norm)
	if u.Scheme != "http" {
		return nil
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return nil
	}
	var out []string
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if port := u.Port(); port != "" {
			host = net.JoinHostPort(host, port)
		} else if host == "::1" {
			host = "[::1]"
		}
		alias := "http://" + host
		if alias != norm {
			out = append(out, alias)
		}
	}
	return out
}
