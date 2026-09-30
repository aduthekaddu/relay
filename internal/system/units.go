package system

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	unitsCacheTTL   = 2 * time.Second
	unitListTimeout = 5 * time.Second
	unitActTimeout  = 30 * time.Second
	managedPrefix   = "relay"
)

// unitNameRE is the set of characters systemd allows in unit names
// (plus the instance separator). A leading '-' is refused separately so
// a name can never be read as an option.
var unitNameRE = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]{1,200}\.service$`)

// ErrNoSystemd reports that the user service manager is unavailable
// (macOS, containers without systemd, no user session bus).
var ErrNoSystemd = errors.New("system: systemd --user is not available")

// Units lists and controls systemd --user services.
type Units struct {
	// Systemctl is the systemctl binary ("" = not installed).
	Systemctl string
	// Run executes argv with a timeout. nil = os/exec (LC_ALL=C).
	Run func(ctx context.Context, argv ...string) ([]byte, error)
	// BootTime returns the boot time, used to turn monotonic timestamps
	// into wall-clock times.
	BootTime func() time.Time

	mu     sync.Mutex
	cache  []api.Service
	cached time.Time
}

// NewUnits returns a Units for the current user.
func NewUnits(bootTime func() time.Time) *Units {
	u := &Units{BootTime: bootTime}
	if p, err := exec.LookPath("systemctl"); err == nil {
		u.Systemctl = p
	}
	return u
}

func (u *Units) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if u.Systemctl == "" {
		return nil, ErrNoSystemd
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{u.Systemctl, "--user", "--no-pager"}, args...)
	if u.Run != nil {
		return u.Run(ctx, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0")
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if msg != "" {
			return out, fmt.Errorf("systemctl %s: %w: %s", args[0], err, msg)
		}
		return out, fmt.Errorf("systemctl %s: %w", args[0], err)
	}
	return out, nil
}

// NormalizeUnit validates a client-supplied unit name, appending
// ".service" when no suffix is given.
func NormalizeUnit(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" && !strings.HasSuffix(name, ".service") && !strings.Contains(name, ".") {
		name += ".service"
	}
	if strings.HasPrefix(name, "-") || !unitNameRE.MatchString(name) {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid service name", Field: "name"}
	}
	return name, nil
}

// Managed reports whether a unit belongs to Relay.
func Managed(name string) bool { return strings.HasPrefix(name, managedPrefix) }

// List returns every user service (loaded, including inactive ones),
// sorted with Relay's units first. Results are cached for 2 s.
func (u *Units) List(ctx context.Context) ([]api.Service, error) {
	u.mu.Lock()
	if u.cache != nil && time.Since(u.cached) < unitsCacheTTL {
		out := append([]api.Service(nil), u.cache...)
		u.mu.Unlock()
		return out, nil
	}
	u.mu.Unlock()

	out, err := u.run(ctx, unitListTimeout, "list-units", "--type=service", "--all", "--no-legend", "--plain")
	if err != nil {
		return nil, err
	}
	svcs := parseListUnits(out)
	if len(svcs) > 0 {
		names := make([]string, len(svcs))
		for i, s := range svcs {
			names[i] = s.Name
		}
		if det, err := u.show(ctx, names); err == nil {
			for i := range svcs {
				if d, ok := det[svcs[i].Name]; ok {
					svcs[i].Since, svcs[i].Restarts = d.Since, d.Restarts
					if svcs[i].Description == "" {
						svcs[i].Description = d.Description
					}
				}
			}
		}
	}
	sortServices(svcs)
	u.mu.Lock()
	u.cache, u.cached = svcs, time.Now()
	u.mu.Unlock()
	return append([]api.Service(nil), svcs...), nil
}

// Get returns one unit's current state (bypassing the list cache).
func (u *Units) Get(ctx context.Context, name string) (*api.Service, error) {
	det, err := u.show(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	s, ok := det[name]
	if !ok || s.load == "not-found" {
		return nil, httpx.NotFound("no such service")
	}
	svc := s.Service
	return &svc, nil
}

// Act runs start, stop or restart on a unit and returns its new state.
func (u *Units) Act(ctx context.Context, name, action string) (*api.Service, error) {
	switch action {
	case "start", "stop", "restart":
	default:
		return nil, httpx.BadRequest("unsupported action")
	}
	if _, err := u.Get(ctx, name); err != nil {
		return nil, err
	}
	if _, err := u.run(ctx, unitActTimeout, action, name); err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.cache = nil
	u.mu.Unlock()
	return u.Get(ctx, name)
}

type unitDetail struct {
	api.Service
	load string
}

var showProps = "--property=Id,Description,LoadState,ActiveState,SubState,NRestarts,ActiveEnterTimestampMonotonic,InactiveEnterTimestampMonotonic"

func (u *Units) show(ctx context.Context, names []string) (map[string]unitDetail, error) {
	args := append([]string{"show", showProps, "--"}, names...)
	out, err := u.run(ctx, unitListTimeout, args...)
	if err != nil {
		return nil, err
	}
	var boot time.Time
	if u.BootTime != nil {
		boot = u.BootTime()
	}
	return parseShow(out, boot), nil
}

// parseListUnits parses `list-units --no-legend --plain` rows:
// UNIT LOAD ACTIVE SUB DESCRIPTION… (a leading "●" marks failed units on
// some systemd versions).
func parseListUnits(b []byte) []api.Service {
	var out []api.Service
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "●"))
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		if f[1] == "not-found" {
			continue
		}
		s := api.Service{
			Name: f[0], Active: f[2], Sub: f[3], User: true, Managed: Managed(f[0]),
		}
		if len(f) > 4 {
			s.Description = strings.Join(f[4:], " ")
		}
		out = append(out, s)
	}
	return out
}

// parseShow parses `systemctl show` output: KEY=VALUE blocks separated by
// blank lines, one per unit. Monotonic timestamps (µs since boot) are
// converted with boot; zero boot leaves Since unset.
func parseShow(b []byte, boot time.Time) map[string]unitDetail {
	out := map[string]unitDetail{}
	var cur unitDetail
	var activeMono, inactiveMono uint64
	flush := func() {
		if cur.Name != "" {
			mono := activeMono
			if cur.Active != "active" && cur.Active != "reloading" && inactiveMono > 0 {
				mono = inactiveMono
			}
			if mono > 0 && !boot.IsZero() {
				cur.Since = boot.Add(time.Duration(mono) * time.Microsecond).UTC()
			}
			cur.User, cur.Managed = true, Managed(cur.Name)
			out[cur.Name] = cur
		}
		cur, activeMono, inactiveMono = unitDetail{}, 0, 0
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "Id":
			cur.Name = v
		case "Description":
			cur.Description = v
		case "LoadState":
			cur.load = v
		case "ActiveState":
			cur.Active = v
		case "SubState":
			cur.Sub = v
		case "NRestarts":
			cur.Restarts, _ = strconv.Atoi(v)
		case "ActiveEnterTimestampMonotonic":
			activeMono, _ = strconv.ParseUint(v, 10, 64)
		case "InactiveEnterTimestampMonotonic":
			inactiveMono, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	flush()
	return out
}

// sortServices puts Relay's units first, then failed, then active, then
// the rest, each alphabetically.
func sortServices(s []api.Service) {
	rank := func(v api.Service) int {
		switch {
		case v.Managed:
			return 0
		case v.Active == "failed":
			return 1
		case v.Active == "active" || v.Active == "activating" || v.Active == "reloading":
			return 2
		}
		return 3
	}
	sort.SliceStable(s, func(i, j int) bool {
		if ri, rj := rank(s[i]), rank(s[j]); ri != rj {
			return ri < rj
		}
		return s[i].Name < s[j].Name
	})
}
