package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// IPifyURL answers with the caller's public IPv4 address.
const IPifyURL = "https://api.ipify.org"

// PublicIP asks url (default IPifyURL) for this machine's public address,
// with a 5 second timeout.
func PublicIP(ctx context.Context, client *http.Client, url string) (net.IP, error) {
	if url == "" {
		url = IPifyURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "relay-setup")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("detect public IP: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("detect public IP: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64))
	if err != nil {
		return nil, fmt.Errorf("detect public IP: %w", err)
	}
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if ip == nil {
		return nil, fmt.Errorf("detect public IP: unexpected answer %q", strings.TrimSpace(string(b)))
	}
	return ip, nil
}

// SslipHost returns the sslip.io name that resolves to ip:
// 203.0.113.7 → 203-0-113-7.sslip.io, 2001:db8::1 → 2001-db8--1.sslip.io.
func SslipHost(ip net.IP) (string, error) {
	if ip == nil {
		return "", errors.New("no IP address")
	}
	if v4 := ip.To4(); v4 != nil {
		if !isPublic(v4) {
			return "", fmt.Errorf("%s is not a public address; sslip.io certificates need a public IP", v4)
		}
		return strings.ReplaceAll(v4.String(), ".", "-") + ".sslip.io", nil
	}
	if !isPublic(ip) {
		return "", fmt.Errorf("%s is not a public address; sslip.io certificates need a public IP", ip)
	}
	s := strings.ReplaceAll(ip.String(), ":", "-")
	// DNS labels cannot start or end with "-": sslip.io wants a 0 there.
	if strings.HasPrefix(s, "-") {
		s = "0" + s
	}
	if strings.HasSuffix(s, "-") {
		s += "0"
	}
	return s + ".sslip.io", nil
}

func isPublic(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // 100.64/10 (CGNAT, Tailscale)
}

// DNSCheck is the result of checking that a domain points at this machine.
type DNSCheck struct {
	Addrs   []net.IP // what the domain resolves to
	Matched bool     // one of them is ours
	Ours    []net.IP // addresses we compared against
}

// CheckDNS resolves domain and compares it against want (public IP) and
// local interface addresses.
func CheckDNS(ctx context.Context, resolve func(context.Context, string) ([]net.IP, error), domain string, want ...net.IP) (DNSCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := resolve(ctx, domain)
	if err != nil {
		return DNSCheck{}, fmt.Errorf("resolve %s: %w", domain, err)
	}
	res := DNSCheck{Addrs: addrs, Ours: append([]net.IP(nil), want...)}
	res.Ours = append(res.Ours, localAddrs()...)
	for _, a := range addrs {
		for _, o := range res.Ours {
			if o != nil && a.Equal(o) {
				res.Matched = true
			}
		}
	}
	return res, nil
}

// Resolve is the default resolver for CheckDNS.
func Resolve(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.IP)
	}
	return out, nil
}

func localAddrs() []net.IP {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, a := range ifaces {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() {
			out = append(out, n.IP)
		}
	}
	return out
}

// ValidDomain reports whether s is a plausible public hostname.
func ValidDomain(s string) error {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if s == "" {
		return errors.New("enter a domain name")
	}
	if len(s) > 253 || !strings.Contains(s, ".") {
		return fmt.Errorf("%q is not a full domain name (e.g. relay.example.com)", s)
	}
	if net.ParseIP(s) != nil {
		return errors.New("enter a name, not an IP address (use the sslip.io option for a bare IP)")
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%q is not a valid domain name", s)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("%q is not a valid domain name", s)
			}
		}
	}
	return nil
}

// Tailscale describes the local tailscale node.
type Tailscale struct {
	Installed bool
	Running   bool
	DNSName   string // machine.tailnet.ts.net (no trailing dot)
}

// DetectTailscale inspects `tailscale status --json`.
func DetectTailscale(ctx context.Context, sys System) Tailscale {
	bin, err := sys.LookPath("tailscale")
	if err != nil {
		return Tailscale{}
	}
	ts := Tailscale{Installed: true}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := sys.Output(ctx, bin, "status", "--json")
	if err != nil {
		return ts
	}
	var st struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if json.Unmarshal([]byte(out), &st) != nil {
		return ts
	}
	ts.Running = st.BackendState == "Running"
	ts.DNSName = strings.TrimSuffix(st.Self.DNSName, ".")
	return ts
}

// TailscaleServeArgs is the command that publishes Relay on the tailnet.
func TailscaleServeArgs(port int) []string {
	return []string{"tailscale", "serve", "--bg", "--https=443", fmt.Sprintf("http://127.0.0.1:%d", port)}
}
