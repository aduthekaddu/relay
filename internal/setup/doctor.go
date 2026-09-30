package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/toolbox"
	"github.com/aduthekaddu/relay/internal/version"
)

// Check statuses.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
	StatusInfo = "info"
)

// Check is one line of `relay doctor`.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Report is the full `relay doctor --json` document.
type Report struct {
	OK      bool    `json:"ok"` // no check failed
	Version string  `json:"version"`
	Checks  []Check `json:"checks"`
}

// Doctor inspects an installation. All probes are injectable.
type Doctor struct {
	Sys      System
	Paths    config.Paths
	Getenv   func(string) string
	Binary   string // default: this executable
	UnitsDir string
	HTTP     *http.Client
	IPURL    string
	Resolve  func(context.Context, string) ([]net.IP, error)
	Health   func(ctx context.Context, socket, path string) (*Health, error)
	DiskFree func(path string) (free uint64, err error)
	Listen   func(network, addr string) (net.Listener, error)
	Catalog  *toolbox.Catalog
	Finder   toolbox.Finder
	// Deadline bounds the whole run.
	Deadline time.Duration
}

func (d *Doctor) defaults() {
	if d.Sys == nil {
		d.Sys = OSSystem{}
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if d.Resolve == nil {
		d.Resolve = Resolve
	}
	if d.Health == nil {
		d.Health = SocketHealth
	}
	if d.DiskFree == nil {
		d.DiskFree = DiskFree
	}
	if d.Listen == nil {
		d.Listen = net.Listen
	}
	if d.Finder.Dirs == nil {
		d.Finder = toolbox.Finder{Dirs: toolbox.SearchPath(d.Paths.Home)}
	}
	if d.UnitsDir == "" {
		d.UnitsDir = DefaultUnitsDir(d.Sys.GOOS(), d.Paths.Home, d.Getenv)
	}
	if d.Deadline <= 0 {
		d.Deadline = 30 * time.Second
	}
}

// Run executes every check.
func (d *Doctor) Run(ctx context.Context) Report {
	d.defaults()
	ctx, cancel := context.WithTimeout(ctx, d.Deadline)
	defer cancel()
	var checks []Check
	add := func(c ...Check) { checks = append(checks, c...) }

	add(d.checkBinary())
	cfg, cfgChecks := d.checkConfig()
	add(cfgChecks...)
	add(d.checkDirs()...)
	ptyd := d.checkSocket(ctx, "ptyd", "Session daemon (ptyd)", d.Paths.PtydSocket, "/v1/health")
	server := d.checkSocket(ctx, "server", "Web server", d.Paths.CtlSocket, "/api/v1/health")
	add(ptyd, server)
	if cfg != nil {
		add(d.checkListen(ctx, cfg, server.Status != StatusFail))
		if c, ok := d.checkDNS(ctx, cfg); ok {
			add(c)
		}
	}
	add(d.checkServices(ctx)...)
	add(d.checkDisk())
	add(d.checkTools(ctx)...)

	r := Report{OK: true, Version: version.Version, Checks: checks}
	for _, c := range checks {
		if c.Status == StatusFail {
			r.OK = false
		}
	}
	return r
}

func (d *Doctor) checkBinary() Check {
	c := Check{ID: "binary", Title: "Relay binary", Status: StatusOK}
	bin := d.Binary
	if bin == "" {
		var err error
		if bin, err = Executable(); err != nil {
			return Check{ID: "binary", Title: "Relay binary", Status: StatusWarn, Detail: err.Error()}
		}
	}
	c.Detail = fmt.Sprintf("%s at %s", version.Version, bin)
	if version.Version == "dev" {
		c.Detail += " (development build)"
	}
	onPath := false
	for _, dir := range filepath.SplitList(d.Getenv("PATH")) {
		if dir != "" && filepath.Clean(dir) == filepath.Dir(bin) {
			onPath = true
		}
	}
	if !onPath {
		c.Status = StatusWarn
		c.Detail += "; its directory is not on PATH"
		c.Fix = fmt.Sprintf(`add it to your shell profile: export PATH="%s:$PATH"`, filepath.Dir(bin))
	}
	return c
}

func (d *Doctor) checkConfig() (*config.Config, []Check) {
	path := d.Paths.ConfigFile
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, []Check{{ID: "config", Title: "Configuration", Status: StatusWarn,
			Detail: path + " does not exist (defaults: local only on 127.0.0.1:7777)", Fix: "run `relay setup`"}}
	}
	if err != nil {
		return nil, []Check{{ID: "config", Title: "Configuration", Status: StatusFail, Detail: err.Error()}}
	}
	cfg, err := config.Load(d.Paths)
	if err != nil {
		return nil, []Check{{ID: "config", Title: "Configuration", Status: StatusFail, Detail: err.Error(),
			Fix: "fix the TOML syntax, or re-create it with `relay setup --force`"}}
	}
	out := []Check{{ID: "config", Title: "Configuration", Status: StatusOK,
		Detail: fmt.Sprintf("%s (listen %s, %s)", path, cfg.Server.Listen, cfg.Origin())}}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		out = append(out, Check{ID: "config-perms", Title: "Configuration permissions", Status: StatusFail,
			Detail: fmt.Sprintf("%s is mode %04o; it holds your password hash", path, perm),
			Fix:    "chmod 600 " + path})
	}
	acct := Check{ID: "account", Title: "Account", Status: StatusOK, Detail: "user " + cfg.Auth.User}
	if cfg.Auth.PasswordHash == "" {
		acct.Status = StatusWarn
		acct.Detail = "no password in relay.toml (an account created in the browser is stored in the database)"
		acct.Fix = "if you cannot sign in, run `relay setup --force`"
	}
	out = append(out, acct)
	return cfg, out
}

func (d *Doctor) checkDirs() []Check {
	var bad []string
	var fixes []string
	for _, dir := range []string{d.Paths.ConfigDir, d.Paths.DataDir, d.Paths.RuntimeDir} {
		st, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue // created on first start
		}
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		if st.Mode().Perm()&0o077 != 0 {
			bad = append(bad, fmt.Sprintf("%s is mode %04o", dir, st.Mode().Perm()))
			fixes = append(fixes, "chmod 700 "+dir)
		}
	}
	if len(bad) > 0 {
		return []Check{{ID: "dirs", Title: "Data directories", Status: StatusFail,
			Detail: strings.Join(bad, "; ") + " (other users could read your data)", Fix: strings.Join(fixes, " && ")}}
	}
	return []Check{{ID: "dirs", Title: "Data directories", Status: StatusOK, Detail: "private (0700): " + d.Paths.DataDir}}
}

func (d *Doctor) checkSocket(ctx context.Context, id, title, socket, path string) Check {
	h, err := d.Health(ctx, socket, path)
	if Answered(err) {
		return Check{ID: id, Title: title, Status: StatusWarn, Detail: "running, but " + path + " answered " + err.Error(),
			Fix: "update Relay (`relay update`) so both processes are the same version"}
	}
	if err != nil {
		fix := "systemctl --user start relay-ptyd relay"
		if d.Sys.GOOS() == "darwin" {
			fix = "relay setup (loads the LaunchAgents)"
		}
		detail := "not reachable at " + socket
		if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
			detail += ": " + err.Error()
		}
		return Check{ID: id, Title: title, Status: StatusFail, Detail: detail, Fix: fix + "; then `relay logs`"}
	}
	detail := "running " + h.Version
	if id == "ptyd" {
		detail += fmt.Sprintf(", %d session(s)", h.Sessions)
	}
	c := Check{ID: id, Title: title, Status: StatusOK, Detail: detail}
	if h.Version != version.Version && version.Version != "dev" {
		c.Status = StatusWarn
		c.Detail += " (this binary is " + version.Version + ")"
		c.Fix = "systemctl --user restart relay"
		if id == "ptyd" {
			c.Fix = "restart relay-ptyd when no important session is running: systemctl --user restart relay-ptyd"
		}
	}
	return c
}

func (d *Doctor) checkListen(ctx context.Context, cfg *config.Config, serverUp bool) Check {
	c := Check{ID: "listen", Title: "Listen address", Status: StatusOK}
	addr := cfg.Server.Listen
	_, port, err := SplitListen(addr)
	if err != nil {
		return Check{ID: "listen", Title: "Listen address", Status: StatusFail, Detail: err.Error(), Fix: "fix server.listen in relay.toml"}
	}
	scope := "public"
	if IsLoopback(addr) {
		scope = "loopback only"
	}
	c.Detail = fmt.Sprintf("%s (%s)", addr, scope)
	if !serverUp {
		ln, err := d.Listen("tcp", addr)
		if err != nil {
			if errors.Is(err, syscall.EACCES) || strings.Contains(err.Error(), "permission denied") {
				c.Status = StatusFail
				c.Detail += ": no permission to bind port " + fmt.Sprint(port)
				c.Fix = "sudo setcap cap_net_bind_service=+ep <relay binary>, or use a port above 1023"
			} else {
				c.Status = StatusFail
				c.Detail += ": in use by another program"
				c.Fix = fmt.Sprintf("find it with `ss -ltnp 'sport = :%d'`, or change server.listen", port)
			}
		} else {
			ln.Close()
			c.Detail += ", free"
		}
	}
	if port < 1024 && d.Sys.GOOS() == "linux" && d.Sys.Getuid() != 0 {
		bin := d.Binary
		if bin == "" {
			bin, _ = Executable()
		}
		if !HasBindCap(ctx, d.Sys, bin) {
			c.Status = StatusFail
			c.Detail += "; the binary lacks cap_net_bind_service"
			c.Fix = "sudo setcap cap_net_bind_service=+ep " + bin + " && systemctl --user restart relay"
		}
	}
	return c
}

func (d *Doctor) checkDNS(ctx context.Context, cfg *config.Config) (Check, bool) {
	if cfg.Server.Domain == "" || cfg.Server.PublicURL != "" {
		return Check{}, false
	}
	c := Check{ID: "dns", Title: "Domain", Status: StatusOK}
	pub, ipErr := PublicIP(ctx, d.HTTP, d.IPURL)
	chk, err := CheckDNS(ctx, d.Resolve, cfg.Server.Domain, pub)
	switch {
	case err != nil:
		c.Status = StatusFail
		c.Detail = err.Error()
		c.Fix = "create an A/AAAA record for " + cfg.Server.Domain + " pointing at this machine"
	case chk.Matched:
		c.Detail = fmt.Sprintf("%s → %s (this machine)", cfg.Server.Domain, joinIPs(chk.Addrs))
		if !cfg.UseAutoTLS() && cfg.Server.TLS != "manual" {
			c.Status = StatusWarn
			c.Detail += "; automatic HTTPS is off (it needs tls = \"auto\" and listen on :443)"
		}
	case ipErr != nil:
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("%s → %s; could not detect the public IP: %v", cfg.Server.Domain, joinIPs(chk.Addrs), ipErr)
	default:
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("%s → %s, but this machine is %s", cfg.Server.Domain, joinIPs(chk.Addrs), pub)
		c.Fix = "point the DNS record at " + pub.String()
	}
	return c, true
}

func (d *Doctor) checkServices(ctx context.Context) []Check {
	m := Manager{Sys: d.Sys, UnitsDir: d.UnitsDir}
	if !m.Installed(SvcServe) && !m.Installed(SvcPtyd) {
		return []Check{{ID: "services", Title: "Background services", Status: StatusWarn,
			Detail: "not installed in " + d.UnitsDir, Fix: "run `relay setup` to install and start them"}}
	}
	if !m.Available(ctx) {
		return []Check{{ID: "services", Title: "Background services", Status: StatusWarn,
			Detail: "installed, but the user service manager is not reachable"}}
	}
	var out []Check
	for _, svc := range []string{SvcPtyd, SvcServe} {
		st := m.State(ctx, svc)
		en := m.Enabled(ctx, svc)
		c := Check{ID: "service-" + svc, Title: "Service " + m.unit(svc), Status: StatusOK, Detail: st}
		if en {
			c.Detail += ", starts automatically"
		}
		switch {
		case st != "active":
			c.Status = StatusFail
			c.Fix = "systemctl --user enable --now " + m.unit(svc) + "; see `relay logs`"
			if d.Sys.GOOS() == "darwin" {
				c.Fix = "relay setup"
			}
		case !en:
			c.Status = StatusWarn
			c.Fix = "systemctl --user enable " + m.unit(svc)
		}
		out = append(out, c)
	}
	if d.Sys.GOOS() == "linux" {
		on, err := Linger(ctx, d.Sys)
		c := Check{ID: "linger", Title: "Linger", Status: StatusOK, Detail: "services keep running after logout"}
		if err != nil || !on {
			c.Status = StatusWarn
			c.Detail = "off: Relay stops when you log out and does not start at boot"
			c.Fix = "sudo loginctl enable-linger " + d.Sys.Username()
		}
		out = append(out, c)
	}
	return out
}

func (d *Doctor) checkDisk() Check {
	c := Check{ID: "disk", Title: "Disk space", Status: StatusOK}
	dir := d.Paths.DataDir
	for dir != "" && dir != "/" {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	free, err := d.DiskFree(dir)
	if err != nil {
		c.Status = StatusWarn
		c.Detail = err.Error()
		return c
	}
	c.Detail = fmt.Sprintf("%s free on %s", humanBytes(free), dir)
	switch {
	case free < 256<<20:
		c.Status = StatusFail
		c.Fix = "free up space: the database, recordings and uploads need room"
	case free < 2<<30:
		c.Status = StatusWarn
		c.Fix = "less than 2 GB free; clean up or lower terminal.record_days"
	}
	return c
}

// toolGroups is what doctor reports about optional software.
var toolGroups = []struct {
	id, title string
	recipes   []string
	required  bool // missing → warn instead of info
}{
	{"tools-core", "Core tools", []string{"git", "ripgrep", "tmux"}, true},
	{"tools-desktop", "Remote desktop", []string{"desktop"}, false},
	{"tools-code", "Browser IDE", []string{"code-server"}, false},
}

func (d *Doctor) checkTools(ctx context.Context) []Check {
	cat := d.Catalog
	if cat == nil {
		var err error
		if cat, err = toolbox.DefaultCatalog(); err != nil {
			return []Check{{ID: "tools", Title: "Tools", Status: StatusWarn, Detail: err.Error()}}
		}
	}
	goos := d.Sys.GOOS()
	var out []Check
	for _, g := range toolGroups {
		var found, missing []string
		for _, id := range g.recipes {
			if id == "git" {
				if _, ok := d.Finder.Find("git"); ok {
					found = append(found, "git")
				} else {
					missing = append(missing, "git")
				}
				continue
			}
			r := cat.Get(id)
			if r == nil || !r.Supports(goos) {
				continue
			}
			if _, ok := d.Finder.Resolve(r); ok {
				found = append(found, r.Name)
			} else {
				missing = append(missing, r.Name)
			}
		}
		if len(found)+len(missing) == 0 {
			continue
		}
		c := Check{ID: g.id, Title: g.title, Status: StatusOK, Detail: strings.Join(found, ", ")}
		if len(missing) > 0 {
			c.Status = StatusInfo
			if g.required {
				c.Status = StatusWarn
			}
			c.Detail = "missing " + strings.Join(missing, ", ")
			if len(found) > 0 {
				c.Detail += "; found " + strings.Join(found, ", ")
			}
			c.Fix = "install from Toolbox in Relay, or `relay setup --components " + componentFor(g.id) + "`"
		}
		out = append(out, c)
	}
	out = append(out, d.checkAgents(ctx, cat, goos))
	return out
}

func componentFor(group string) string {
	switch group {
	case "tools-desktop":
		return "desktop"
	case "tools-code":
		return "code"
	}
	return "tools"
}

func (d *Doctor) checkAgents(ctx context.Context, cat *toolbox.Catalog, goos string) Check {
	var rs []*toolbox.Recipe
	for _, r := range cat.InCategory("agents") {
		if r.Supports(goos) {
			rs = append(rs, r)
		}
	}
	p := &toolbox.Prober{Finder: d.Finder, Run: toolbox.ExecRunner, Timeout: 4 * time.Second, Concurrency: 4}
	sts := p.ProbeAll(ctx, rs)
	var found []string
	for i, st := range sts {
		if st.Installed {
			n := rs[i].Name
			if st.Version != "" {
				n += " " + st.Version
			}
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return Check{ID: "agents", Title: "Coding agents", Status: StatusInfo, Detail: "none installed",
			Fix: "install one from Toolbox in Relay, or `relay setup --components agents`"}
	}
	return Check{ID: "agents", Title: "Coding agents", Status: StatusOK, Detail: strings.Join(found, ", ")}
}

// DiskFree returns the bytes available to unprivileged users on the file
// system holding path.
func DiskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert // field types differ per OS
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

// PrintReport writes the human-readable doctor output.
func PrintReport(u *UI, r Report) {
	w := 0
	for _, c := range r.Checks {
		if len(c.Title) > w {
			w = len(c.Title)
		}
	}
	for _, c := range r.Checks {
		var mark string
		switch c.Status {
		case StatusOK:
			mark = u.style(sOK, "✓")
		case StatusWarn:
			mark = u.style(sWarn, "⚠")
		case StatusFail:
			mark = u.style(sErr, "✗")
		default:
			mark = u.style(sDim, "·")
		}
		u.Printf("  %s %s  %s\n", mark, pad(c.Title, w), c.Detail)
		if c.Fix != "" && c.Status != StatusOK {
			u.Printf("    %s %s\n", strings.Repeat(" ", w), u.style(sCyan, "→ "+c.Fix))
		}
	}
	counts := map[string]int{}
	for _, c := range r.Checks {
		counts[c.Status]++
	}
	u.Printf("\n  %d ok, %d warning(s), %d problem(s)\n", counts[StatusOK], counts[StatusWarn], counts[StatusFail])
}

// WriteJSON writes the report as indented JSON.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
