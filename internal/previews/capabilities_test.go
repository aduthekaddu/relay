package previews

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

type capabilityResolver func(context.Context, string) ([]string, error)

func (f capabilityResolver) LookupHost(ctx context.Context, h string) ([]string, error) {
	return f(ctx, h)
}

func TestPreviewCapabilityNormalizedDecisions(t *testing.T) {
	for _, tc := range []struct{ name, configured, host, origin, mode, detection, url string }{
		{"localhost", "auto", "localhost", "http://localhost:47790", "subdomain", "localhost", "http://47791.localhost:47790/"},
		{"localhost-normalized", " AUTO ", " LOCALHOST.:47790 ", "http://LOCALHOST.:47790", "subdomain", "localhost", "http://47791.localhost:47790/"},
		{"localhost-subdomain", "auto", " Dev.Localhost. ", "http://dev.localhost:47790", "subdomain", "localhost", "http://47791.dev.localhost:47790/"},
		{"absent-explicit", "subdomain", "", "https://relay.example.test", "path", "missing-host", "https://relay.example.test/p/47791/"},
		{"https-pending", "auto", "relay.example.test", "https://relay.example.test", "path", "pending", "https://relay.example.test/p/47791/"},
		{"ipv4", "auto", "192.0.2.1", "http://192.0.2.1:47790", "path", "ip-literal", "http://192.0.2.1:47790/p/47791/"},
		{"ipv4-explicit", "subdomain", "192.0.2.1:47792", "http://192.0.2.1:47790", "path", "ip-literal", "http://192.0.2.1:47790/p/47791/"},
		{"ipv6", "auto", "[2001:db8::1]", "https://[2001:db8::1]", "path", "ip-literal", "https://[2001:db8::1]/p/47791/"},
		{"ipv6-port", "subdomain", "[2001:db8::1]:47792", "https://[2001:db8::1]:47790", "path", "ip-literal", "https://[2001:db8::1]:47790/p/47791/"},
		{"explicit-normalized", " Subdomain ", " DEV.Example.Test.:47792 ", "https://relay.example.test:47790", "subdomain", "explicit", "https://47791.dev.example.test:47792/"},
		{"invalid-host", "auto", "https://bad.example.test", "http://localhost:47790", "path", "invalid-host", "http://localhost:47790/p/47791/"},
		{"bad-port", "subdomain", "localhost:99999", "http://localhost:47790", "path", "invalid-host", "http://localhost:47790/p/47791/"},
		{"path", "path", "dev.example.test", "http://localhost:47790", "path", "not-required", "http://localhost:47790/p/47791/"},
		{"off", "off", "localhost", "http://localhost:47790", "off", "off", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.configured, tc.host, tc.origin)
			var calls atomic.Int64
			h.svc.res = capabilityResolver(func(context.Context, string) ([]string, error) {
				calls.Add(1)
				return nil, errors.New("synthetic NXDOMAIN")
			})
			h.svc.mu.Lock()
			h.svc.entries[47791] = &entry{port: 47791}
			h.svc.mu.Unlock()
			cap := h.svc.PreviewCapability()
			p, _ := h.svc.get(47791)
			if cap.EffectiveMode != tc.mode || cap.Detection != tc.detection || p.URL != tc.url {
				t.Fatalf("decision=%+v url=%q", cap, p.URL)
			}
			if calls.Load() != 0 {
				t.Fatal("query performed DNS")
			}
			if tc.mode == "off" && len(h.svc.List()) != 0 {
				t.Fatal("disabled previews listed")
			}
			if tc.mode == "subdomain" {
				u := strings.TrimSuffix(strings.TrimPrefix(tc.url, h.svc.origin.Scheme+"://"), "/")
				if port, ok := parsePreviewHost(u, cap.Host); !ok || port != 47791 {
					t.Fatal("generated URL is not recognized by host dispatch")
				}
			}
		})
	}
}

func TestPreviewCapabilityDNSRechecksRefreshURLsAndEvents(t *testing.T) {
	h := newHarness(t, "auto", " DEV.Example.Test. ", "https://relay.example.test:47790")
	h.svc.mu.Lock()
	h.svc.entries[47791] = &entry{port: 47791}
	h.svc.mu.Unlock()
	sub := h.bus.Subscribe(32, nil)
	defer sub.Close()
	for _, tc := range []struct {
		name            string
		res             resolver
		mode, detection string
	}{
		{"wildcard", fakeResolver{"dev.example.test": {"192.0.2.1"}, "*": {"192.0.2.1"}}, "subdomain", "verified"},
		{"address-mismatch", fakeResolver{"dev.example.test": {"192.0.2.1"}, "*": {"198.51.100.1"}}, "path", "address-mismatch"},
		{"wildcard-restored", fakeResolver{"dev.example.test": {"192.0.2.1"}, "*": {"192.0.2.1"}}, "subdomain", "verified"},
		{"NXDOMAIN", fakeResolver{}, "path", "lookup-failed"},
		{"timeout", capabilityResolver(func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsTimeout: true} }), "path", "timeout"},
	} {
		old := h.svc.Mode()
		h.svc.res = tc.res
		h.svc.redetectMode(t.Context())
		cap := h.svc.PreviewCapability()
		if cap.EffectiveMode != tc.mode || cap.Detection != tc.detection || cap.CheckedAt == nil {
			t.Fatalf("%s: %+v", tc.name, cap)
		}
		checkedAt := *cap.CheckedAt
		*cap.CheckedAt = cap.CheckedAt.Add(time.Hour)
		if !h.svc.PreviewCapability().CheckedAt.Equal(checkedAt) {
			t.Fatal("caller mutated the owner's snapshot")
		}
		want := "https://relay.example.test:47790/p/47791/"
		if tc.mode == "subdomain" {
			want = "https://47791.dev.example.test:47790/"
		}
		list := h.svc.List()
		if len(list) != 1 || list[0].URL != want {
			t.Fatalf("%s: stale URL", tc.name)
		}
		gotCap, gotList := false, false
		for {
			select {
			case ev := <-sub.C:
				if ev.Type == api.EvCapabilitiesChanged {
					gotCap = true
				}
				if ev.Type == api.EvPreviewsChanged {
					gotList = true
					data, ok := ev.Data.([]api.Preview)
					if !ok || len(data) != 1 || data[0].URL != want {
						t.Fatal("mode event retained old URLs")
					}
				}
			default:
				goto drained
			}
		}
	drained:
		if !gotCap || (old != tc.mode && !gotList) {
			t.Fatalf("%s: missing immediate change event", tc.name)
		}
		// /link must report its URL and mode from one locked decision.
		r := httptest.NewRequest("GET", "/api/v1/previews/47791/link", nil)
		r.Header.Set("Authorization", "Bearer rly_good")
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		var link api.PreviewLink
		if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || link.Mode != tc.mode || link.URL != want {
			t.Fatalf("inconsistent link: %d %+v", w.Code, link)
		}
	}
}

func TestPreviewResolverDeadlineAndCancellation(t *testing.T) {
	var calls int
	res := capabilityResolver(func(ctx context.Context, h string) ([]string, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > dnsTimeout {
			t.Error("DNS has no bounded deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	mode, detection := detectModeResult(ctx, "auto", "dev.example.test", res)
	if mode != "path" || detection != "timeout" || calls != 1 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("timeout mode=%s detection=%s calls=%d", mode, detection, calls)
	}
	h := newHarness(t, "auto", "dev.example.test", "https://dev.example.test")
	h.svc.res = fakeResolver{"dev.example.test": {"192.0.2.1"}, "*": {"192.0.2.1"}}
	h.svc.redetectMode(t.Context())
	old := h.svc.PreviewCapability()
	canceled, stop := context.WithCancel(t.Context())
	stop()
	h.svc.redetectMode(canceled)
	if got := h.svc.PreviewCapability(); got.EffectiveMode != old.EffectiveMode || !got.CheckedAt.Equal(*old.CheckedAt) {
		t.Fatal("cancellation replaced last effective decision")
	}
}

func TestPreviewConcurrentSnapshotsAndRechecks(t *testing.T) {
	h := newHarness(t, "auto", "dev.example.test", "https://dev.example.test:47790")
	h.svc.mu.Lock()
	h.svc.entries[47791] = &entry{port: 47791}
	h.svc.mu.Unlock()
	var calls atomic.Int64
	h.svc.res = capabilityResolver(func(context.Context, string) ([]string, error) {
		if calls.Add(1)%4 == 0 {
			return []string{"198.51.100.1"}, nil
		}
		return []string{"192.0.2.1"}, nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				list := h.svc.List()
				if len(list) != 1 || (list[0].URL != "https://dev.example.test:47790/p/47791/" && list[0].URL != "https://47791.dev.example.test:47790/") {
					t.Error("mixed preview decision")
				}
				h.svc.PreviewCapability()
			}
		}()
	}
	for range 100 {
		h.svc.redetectMode(t.Context())
	}
	wg.Wait()
}

func TestPreviewAddressSetNormalization(t *testing.T) {
	for _, tc := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"192.0.2.1", "2001:db8::1"}, []string{"2001:0db8:0:0:0:0:0:1", "::ffff:192.0.2.1"}, true},
		{[]string{"192.0.2.1", "192.0.2.1"}, []string{"192.0.2.1"}, true},
		{[]string{"192.0.2.1"}, []string{"198.51.100.1"}, false},
		{nil, nil, false}, {[]string{"invalid"}, []string{"invalid"}, false},
	} {
		if got := sameAddrs(tc.a, tc.b); got != tc.want {
			t.Errorf("address equivalence=%v want %v", got, tc.want)
		}
	}
}

func TestPreviewWildcardLookupFailuresShareDeadline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result []string
		err    error
		want   string
	}{
		{"empty-wildcard", nil, nil, "lookup-failed"},
		{"NXDOMAIN-wildcard", nil, &net.DNSError{IsNotFound: true}, "lookup-failed"},
		{"timeout-wildcard", nil, &net.DNSError{IsTimeout: true}, "timeout"},
		{"invalid-address", []string{"invalid"}, nil, "address-mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			var firstDeadline time.Time
			res := capabilityResolver(func(ctx context.Context, _ string) ([]string, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("Missing DNS deadline")
				}
				if calls == 1 {
					firstDeadline = deadline
					return []string{"192.0.2.1"}, nil
				}
				if !deadline.Equal(firstDeadline) {
					t.Fatal("Second lookup gained another timeout budget")
				}
				return tc.result, tc.err
			})
			mode, detection := detectModeResult(t.Context(), "auto", "dev.example.test", res)
			if mode != "path" || detection != tc.want || calls != 2 {
				t.Fatalf("%s/%s calls=%d", mode, detection, calls)
			}
		})
	}
}
