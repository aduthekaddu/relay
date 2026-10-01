// Package previews detects the user's listening dev servers and serves them
// as authenticated previews, either under /p/<port>/ on the Relay origin
// (inside a CSP sandbox) or on their own origin https://<port>.<host>
// (host-only cookie obtained through a signed handshake).
//
// Detection reads /proc/net/tcp{,6} (lsof on macOS) every 2 s, attributes
// sockets to the user's own processes, probes HTTP, and publishes
// api.EvPreviewsChanged when the list changes. A new HTTP server that
// stays up for 3 s raises a "preview" notification.
package previews

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/previews/revproxy"
	"github.com/aduthekaddu/relay/internal/server"
)

// Tunables.
const (
	pollInterval    = 2 * time.Second
	notifyAfter     = 3 * time.Second
	notifyCooldown  = 10 * time.Minute
	reprobeHTTP     = 30 * time.Second
	reprobeNonHTTP  = 4 * time.Second // doubled per miss, up to reprobeMax
	reprobeMax      = 5 * time.Minute
	modeRecheck     = 10 * time.Minute
	onDemandMinScan = time.Second
	probeParallel   = 4
)

// entry is the live state of one listening port.
type entry struct {
	port      int
	ip        net.IP
	key       uint64
	info      procInfo
	workspace string
	probe     probeResult
	framework string
	probedAt  time.Time
	misses    int // consecutive non-HTTP probes (backoff)
	firstSeen time.Time
	notified  bool
}

// Service is the previews feature.
type Service struct {
	d       *core.Deps
	log     *slog.Logger
	src     source
	prober  *prober
	meta    metaStore
	tok     *tokens
	origin  origin
	selfPID int
	now     func() time.Time
	res     resolver
	rt      *server.Router

	mode atomic.Value // api.PreviewCapability; written under mu

	scanMu   sync.Mutex // serialises scans
	mu       sync.Mutex // guards the fields below
	entries  map[int]*entry
	metas    map[int]meta
	lastList []api.Preview
	scanned  bool
	lastScan time.Time
	notified map[int]time.Time
	proxies  map[string]*revproxy.Proxy
}

// New constructs the service: migrates its table and loads the HMAC key.
func New(d *core.Deps) (*Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := &Service{
		d:        d,
		log:      d.Log,
		prober:   newProber(),
		meta:     metaStore{st: d.Store},
		origin:   parseOrigin(d.Cfg.Origin()),
		selfPID:  os.Getpid(),
		now:      time.Now,
		res:      net.DefaultResolver,
		entries:  map[int]*entry{},
		metas:    map[int]meta{},
		notified: map[int]time.Time{},
		proxies:  map[string]*revproxy.Proxy{},
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.src = defaultSource()
	if err := s.meta.migrate(ctx); err != nil {
		return nil, fmt.Errorf("previews: %w", err)
	}
	m, err := s.meta.all(ctx)
	if err != nil {
		return nil, err
	}
	s.metas = m
	if s.tok, err = loadTokens(ctx, d.Store); err != nil {
		return nil, err
	}
	s.mode.Store(s.initialCapability())
	return s, nil
}

func defaultSource() source {
	fs := newProcFS("/proc", os.Getuid())
	if fs.available() {
		return &procSource{fs: fs}
	}
	if bin, err := exec.LookPath("lsof"); err == nil {
		return &lsofSource{uid: os.Getuid(), bin: bin}
	}
	return emptySource{}
}

// Start runs the detection loop until ctx is cancelled.
func (s *Service) Start(ctx context.Context) error {
	if s.Mode() == modeOff {
		<-ctx.Done()
		return ctx.Err()
	}
	s.redetectMode(ctx)
	s.scan(ctx)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	lastMode := s.now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if s.now().Sub(lastMode) >= modeRecheck {
				lastMode = s.now()
				s.redetectMode(ctx)
			}
			s.scan(ctx)
		}
	}
}

// Close releases proxy connections.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.proxies {
		p.Close()
		delete(s.proxies, k)
	}
	return nil
}

// excluded reports whether port must never be listed or proxied.
func (s *Service) excluded(port int) bool {
	cfg := s.d.Cfg
	if port < cfg.Previews.PortMin || port > cfg.Previews.PortMax {
		return true
	}
	for _, p := range cfg.Previews.Ignore {
		if p == port {
			return true
		}
	}
	if _, ps, err := net.SplitHostPort(cfg.Server.Listen); err == nil {
		if lp, _ := strconv.Atoi(ps); lp == port {
			return true
		}
	}
	return false
}

// scan refreshes the port table once, probes what needs probing,
// publishes changes and raises notifications.
func (s *Service) scan(ctx context.Context) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	socks, err := s.src.sockets(ctx)
	if err != nil {
		s.log.Debug("previews scan failed", "err", err)
		return
	}
	now := s.now()
	chosen := map[int]socket{}
	for _, so := range socks {
		if so.PID == s.selfPID || s.excluded(so.Port) {
			continue
		}
		if cur, ok := chosen[so.Port]; !ok || addrRank(so.IP) > addrRank(cur.IP) {
			chosen[so.Port] = so
		}
	}

	s.mu.Lock()
	old := s.entries
	first := !s.scanned
	s.mu.Unlock()

	next := make(map[int]*entry, len(chosen))
	var toProbe []*entry
	for port, so := range chosen {
		e := old[port]
		if e == nil || e.key != so.Key {
			e = &entry{port: port, key: so.Key, firstSeen: now, notified: first}
			if so.PID > 0 {
				e.info = s.src.info(ctx, so.PID)
				if s.d.Workspaces != nil && e.info.Cwd != "" {
					e.workspace = s.d.Workspaces.RootOf(e.info.Cwd)
				}
			}
		} else {
			cp := *e
			e = &cp
		}
		e.ip = so.IP
		if e.probedAt.IsZero() ||
			(e.probe.HTTP && now.Sub(e.probedAt) >= reprobeHTTP) ||
			(!e.probe.HTTP && now.Sub(e.probedAt) >= nonHTTPBackoff(e.misses)) {
			toProbe = append(toProbe, e)
		}
		next[port] = e
	}
	s.runProbes(ctx, toProbe, now)

	s.mu.Lock()
	s.entries = next
	s.scanned = true
	s.lastScan = now
	for k, p := range s.proxies {
		if port, _ := strconv.Atoi(k[2:]); next[port] == nil {
			p.Close()
			delete(s.proxies, k)
		}
	}
	list := s.listLocked()
	changed := !reflect.DeepEqual(list, s.lastList)
	if changed {
		s.lastList = list
	}
	due := s.dueNotificationsLocked(now)
	s.mu.Unlock()

	if changed {
		s.d.Bus.Publish(api.EvPreviewsChanged, list)
	}
	for _, n := range due {
		s.sendNotification(ctx, n)
	}
}

// addrRank prefers wildcard binds, then IPv4 loopback, then the rest.
func addrRank(ip net.IP) int {
	switch {
	case ip.IsUnspecified():
		return 3
	case ip.IsLoopback() && ip.To4() != nil:
		return 2
	case ip.IsLoopback():
		return 1
	}
	return 0
}

func (s *Service) runProbes(ctx context.Context, es []*entry, now time.Time) {
	if len(es) == 0 {
		return
	}
	sem := make(chan struct{}, probeParallel)
	var wg sync.WaitGroup
	for _, e := range es {
		wg.Add(1)
		sem <- struct{}{}
		go func(e *entry) {
			defer wg.Done()
			defer func() { <-sem }()
			e.probe = s.prober.probe(ctx, probeHost(e.ip), e.port)
			e.probedAt = now
			if e.probe.HTTP {
				e.misses = 0
			} else {
				e.misses++
			}
			e.framework = e.probe.Framework
			if e.framework == "" {
				e.framework = detectFramework(nil, nil, e.info.Cmdline)
			}
		}(e)
	}
	wg.Wait()
}

// nonHTTPBackoff is how long to wait before re-probing a port that did
// not answer HTTP: a dev server that is still booting is found quickly,
// while databases and other services are not hammered every few seconds.
func nonHTTPBackoff(misses int) time.Duration {
	d := reprobeNonHTTP
	for i := 1; i < misses && d < reprobeMax; i++ {
		d *= 2
	}
	return min(d, reprobeMax)
}

// probeHost is the loopback address that reaches a socket bound to ip.
func probeHost(ip net.IP) string {
	if ip != nil && ip.To4() == nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return "::1"
	}
	return "127.0.0.1"
}

type pendingNotice struct {
	port  int
	title string
	body  string
}

func (s *Service) dueNotificationsLocked(now time.Time) []pendingNotice {
	var out []pendingNotice
	for port, e := range s.entries {
		if e.notified || now.Sub(e.firstSeen) < notifyAfter {
			continue
		}
		if !e.probe.HTTP {
			if now.Sub(e.firstSeen) > time.Minute {
				e.notified = true // never became HTTP: not a preview
			}
			continue
		}
		e.notified = true
		m := s.metas[port]
		if m.Hidden {
			continue
		}
		if last, ok := s.notified[port]; ok && now.Sub(last) < notifyCooldown {
			continue
		}
		s.notified[port] = now
		out = append(out, pendingNotice{port: port, title: fmt.Sprintf("%s on :%d", displayName(e, m), port), body: noticeBody(e)})
	}
	for port, t := range s.notified {
		if now.Sub(t) > notifyCooldown {
			delete(s.notified, port)
		}
	}
	return out
}

func displayName(e *entry, m meta) string {
	switch {
	case m.Label != "":
		return m.Label
	case e.framework != "":
		return e.framework
	case e.info.Exe != "":
		return e.info.Exe
	}
	return "Dev server"
}

func noticeBody(e *entry) string {
	var parts []string
	if e.probe.Title != "" {
		parts = append(parts, e.probe.Title)
	}
	if e.workspace != "" {
		parts = append(parts, lastPathElem(e.workspace))
	}
	return strings.Join(parts, " · ")
}

func (s *Service) sendNotification(ctx context.Context, n pendingNotice) {
	if s.d.Notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.d.Notifier.Notify(ctx, api.NotifyRequest{
		Kind:     "preview",
		Title:    n.title,
		Body:     n.body,
		Link:     "/previews?port=" + strconv.Itoa(n.port),
		Severity: "info",
	})
	if err != nil {
		s.log.Debug("preview notification failed", "port", n.port, "err", err)
	}
}

// List returns the current previews: pinned first, then by port.
func (s *Service) List() []api.Preview {
	if s.Mode() == modeOff {
		return []api.Preview{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Service) listLocked() []api.Preview {
	if s.Mode() == modeOff {
		return []api.Preview{}
	}
	out := make([]api.Preview, 0, len(s.entries))
	for port, e := range s.entries {
		out = append(out, s.previewLocked(port, e))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].Port < out[j].Port
	})
	return out
}

func (s *Service) previewLocked(port int, e *entry) api.Preview {
	m := s.metas[port]
	p := api.Preview{
		Port:   port,
		URL:    s.urlFor(s.Mode(), port),
		Pinned: m.Pinned,
		Hidden: m.Hidden,
		Label:  m.Label,
	}
	if e == nil {
		return p
	}
	p.Address = e.ip.String()
	p.PID = e.info.PID
	p.Process = e.info.Exe
	p.Cwd = e.info.Cwd
	p.Workspace = e.workspace
	p.HTTP = e.probe.HTTP
	p.Title = e.probe.Title
	p.FirstSeenAt = e.firstSeen.UTC()
	if p.Label == "" {
		p.Label = e.framework
	}
	return p
}

// get returns the preview for port and whether it is listening.
func (s *Service) get(port int) (api.Preview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[port]
	return s.previewLocked(port, e), e != nil
}

// update applies a PATCH.
func (s *Service) update(ctx context.Context, port int, req api.UpdatePreviewRequest) (api.Preview, error) {
	s.mu.Lock()
	m := s.metas[port]
	if req.Label != nil {
		m.Label = capString(strings.TrimSpace(*req.Label), 80)
	}
	if req.Pinned != nil {
		m.Pinned = *req.Pinned
	}
	if req.Hidden != nil {
		m.Hidden = *req.Hidden
	}
	s.mu.Unlock()
	if err := s.meta.put(ctx, port, m); err != nil {
		return api.Preview{}, err
	}
	s.mu.Lock()
	if m == (meta{}) {
		delete(s.metas, port)
	} else {
		s.metas[port] = m
	}
	list := s.listLocked()
	s.lastList = list
	p := s.previewLocked(port, s.entries[port])
	s.mu.Unlock()
	s.d.Bus.Publish(api.EvPreviewsChanged, list)
	return p, nil
}

// listening reports whether port is a detected, proxyable preview. On a
// miss it rescans (at most once per second) so a server that started
// between polls is reachable immediately.
func (s *Service) listening(ctx context.Context, port int) bool {
	if s.excluded(port) {
		return false
	}
	s.mu.Lock()
	_, ok := s.entries[port]
	fresh := s.now().Sub(s.lastScan) < onDemandMinScan
	s.mu.Unlock()
	if ok || fresh {
		return ok
	}
	s.scan(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok = s.entries[port]
	return ok
}

// TLSHostAllowed is the autocert host policy for preview subdomains:
// only ports that are currently listening get a certificate, so random
// hostnames cannot burn the ACME rate limit.
func (s *Service) TLSHostAllowed(host string) bool {
	if s.Mode() != modeSubdomain {
		return false
	}
	port, ok := parsePreviewHost(host, s.baseHost())
	if !ok {
		return false
	}
	return s.listening(context.Background(), port)
}
