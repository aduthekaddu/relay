package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/toolbox"
	"github.com/aduthekaddu/relay/internal/version"
)

// Options are the command-line answers for `relay setup`. Empty fields
// are asked interactively (or take their default with --yes).
type Options struct {
	Access        string
	Domain        string
	Email         string
	Listen        string
	PublicURL     string
	User          string
	PasswordStdin bool
	Stdin         io.Reader // source for --password-stdin
	Components    string
	ComponentsSet bool // --components was given (even empty)
	Yes           bool // accept defaults, never prompt
	Force         bool // replace an existing relay.toml (after a backup)
	NoSystemd     bool // do not talk to systemd/launchd
	NoStart       bool // write units but do not start them
	UnitsDir      string
	Binary        string // absolute path baked into units; default: this executable
}

// Wizard runs the setup flow. Every side effect goes through Sys, the
// network hooks, or the paths, so tests can drive it end to end.
type Wizard struct {
	Opt     Options
	UI      *UI
	Sys     System
	Paths   config.Paths
	Getenv  func(string) string
	Now     func() time.Time
	HTTP    *http.Client
	IPURL   string // public IP service (default IPifyURL)
	Resolve func(context.Context, string) ([]net.IP, error)
	Catalog *toolbox.Catalog
	Finder  toolbox.Finder
	// HealthTimeout bounds the wait for the server after starting it.
	HealthTimeout time.Duration

	answers   Answers
	generated string // generated password, shown once at the end
	cfg       *config.Config
	started   bool
}

// Result summarizes a finished setup.
type Result struct {
	URL        string
	ConfigFile string
	Units      []string
	Started    bool
}

const totalSteps = 5

// Run executes the wizard.
func (w *Wizard) Run(ctx context.Context) (*Result, error) {
	w.defaults()
	u := w.UI
	u.Banner("Relay setup", fmt.Sprintf("Version %s. Your machine, from any browser — this takes about a minute.", version.Version))
	if !u.Interactive {
		u.Dim("Non-interactive mode: using flags and safe defaults.")
	}

	keep, err := w.existingConfig()
	if err != nil {
		return nil, err
	}
	if !keep {
		u.Step(1, totalSteps, "How will you reach this machine?")
		if err := w.access(ctx); err != nil {
			return nil, err
		}
		u.Step(2, totalSteps, "Your account")
		if err := w.account(); err != nil {
			return nil, err
		}
	}
	u.Step(3, totalSteps, "Optional components")
	if err := w.components(ctx); err != nil {
		w.revealPassword()
		return nil, err
	}
	if !keep {
		if err := w.writeConfig(); err != nil {
			return nil, err
		}
	}
	u.Step(4, totalSteps, "Background services")
	units, err := w.services(ctx)
	if err != nil {
		w.revealPassword()
		return nil, err
	}
	u.Step(5, totalSteps, "Done")
	res := &Result{URL: w.cfg.Origin(), ConfigFile: w.Paths.ConfigFile, Units: units, Started: w.started}
	w.final(res)
	return res, nil
}

func (w *Wizard) defaults() {
	if w.Sys == nil {
		w.Sys = OSSystem{}
	}
	if w.Getenv == nil {
		w.Getenv = os.Getenv
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if w.Resolve == nil {
		w.Resolve = Resolve
	}
	if w.HealthTimeout <= 0 {
		w.HealthTimeout = 20 * time.Second
	}
	if w.Finder.Dirs == nil {
		w.Finder = toolbox.Finder{Dirs: toolbox.SearchPath(w.Paths.Home)}
	}
}

// existingConfig decides whether to keep a relay.toml that is already
// there. Without --force the default is always to keep it.
func (w *Wizard) existingConfig() (keep bool, err error) {
	_, statErr := os.Stat(w.Paths.ConfigFile)
	if errors.Is(statErr, fs.ErrNotExist) {
		return false, nil
	}
	if statErr != nil {
		return false, fmt.Errorf("check %s: %w", w.Paths.ConfigFile, statErr)
	}
	cfg, loadErr := config.Load(w.Paths)
	u := w.UI
	if !w.Opt.Force {
		if loadErr != nil {
			u.Warn("%s exists but does not load: %v", w.Paths.ConfigFile, loadErr)
		} else {
			u.OK("Found your configuration at %s", w.Paths.ConfigFile)
			u.Dim("Listening on %s, reachable at %s", cfg.Server.Listen, cfg.Origin())
		}
		idx, err := u.Choose("Keep it?", []Choice{
			{Label: "Keep my configuration", Hint: "reinstall services and components only"},
			{Label: "Start over", Hint: "the current file is backed up first"},
		}, 0)
		if err != nil {
			return false, err
		}
		if idx == 0 {
			if loadErr != nil {
				return false, fmt.Errorf("cannot keep %s: %w (run setup again and choose Start over, or fix the file)", w.Paths.ConfigFile, loadErr)
			}
			w.cfg = cfg
			return true, nil
		}
	}
	return false, nil
}

var accessChoices = map[string]Choice{
	AccessTailscale: {Label: "Tailscale", Hint: "private: only your devices on your tailnet, HTTPS by Tailscale"},
	AccessDomain:    {Label: "My domain", Hint: "public HTTPS on :443 with a Let's Encrypt certificate"},
	AccessSslip:     {Label: "Instant domain", Hint: "https://<your-ip>.sslip.io with a real certificate, no DNS needed"},
	AccessProxy:     {Label: "My own proxy", Hint: "Caddy/nginx/Cloudflare Tunnel in front; Relay stays on loopback"},
	AccessLocal:     {Label: "This machine only", Hint: "http://127.0.0.1:7777, e.g. through an SSH tunnel"},
}

func (w *Wizard) access(ctx context.Context) error {
	u := w.UI
	a := &w.answers
	a.Access = strings.ToLower(strings.TrimSpace(w.Opt.Access))
	ts := DetectTailscale(ctx, w.Sys)
	if a.Access == "" {
		def := AccessLocal
		if u.Interactive {
			def = AccessSslip
			if ts.Running {
				def = AccessTailscale
			}
		}
		opts := make([]Choice, len(AccessModes))
		defIdx := 0
		for i, m := range AccessModes {
			opts[i] = accessChoices[m]
			if m == def {
				defIdx = i
			}
		}
		if ts.Running {
			opts[0].Hint += " (detected: " + ts.DNSName + ")"
		}
		idx, err := u.Choose("Access mode", opts, defIdx)
		if err != nil {
			return err
		}
		a.Access = AccessModes[idx]
	}
	a.Listen = w.Opt.Listen
	a.Email = w.Opt.Email
	a.PublicURL = w.Opt.PublicURL
	switch a.Access {
	case AccessTailscale:
		w.accessTailscale(ctx, ts)
	case AccessDomain:
		return w.accessDomain(ctx)
	case AccessSslip:
		return w.accessSslip(ctx)
	case AccessProxy:
		return w.accessProxy()
	case AccessLocal:
		u.OK("Relay will listen on %s only", firstNonEmpty(a.Listen, DefaultLocalListen))
		u.Dim("From another computer: ssh -L 7777:127.0.0.1:7777 <this-machine>, then open http://localhost:7777")
	default:
		return fmt.Errorf("unknown access mode %q (use %s)", a.Access, strings.Join(AccessModes, ", "))
	}
	return nil
}

func (w *Wizard) listenPort() int {
	_, port, err := SplitListen(firstNonEmpty(w.answers.Listen, DefaultLocalListen))
	if err != nil {
		return 7777
	}
	return port
}

func (w *Wizard) accessTailscale(ctx context.Context, ts Tailscale) {
	u := w.UI
	args := TailscaleServeArgs(w.listenPort())
	switch {
	case !ts.Installed:
		u.Warn("Tailscale is not installed yet: https://tailscale.com/download")
		u.Info("After installing and signing in, publish Relay on your tailnet with:")
		u.Code(strings.Join(args, " "))
		return
	case !ts.Running:
		u.Warn("Tailscale is installed but not connected. Run `tailscale up`, then:")
		u.Code(strings.Join(args, " "))
		return
	}
	if w.answers.PublicURL == "" && ts.DNSName != "" {
		w.answers.PublicURL = "https://" + ts.DNSName
	}
	u.OK("Tailscale is connected as %s", ts.DNSName)
	if !u.Interactive {
		u.Info("Publish Relay on your tailnet (HTTPS by Tailscale) with:")
		u.Code(strings.Join(args, " "))
		return
	}
	run, err := u.Confirm("Run `"+strings.Join(args, " ")+"` now?", true)
	if err != nil || !run {
		u.Info("Run it later with:")
		u.Code(strings.Join(args, " "))
		return
	}
	if err := w.Sys.Interactive(ctx, nil, args[0], args[1:]...); err != nil {
		u.Warn("tailscale serve failed (%v). It may need operator rights:", err)
		u.Code("sudo " + strings.Join(args, " "))
		return
	}
	u.OK("Published on your tailnet")
}

func (w *Wizard) accessDomain(ctx context.Context) error {
	u := w.UI
	a := &w.answers
	domain := w.Opt.Domain
	for attempt := 0; attempt < 3; attempt++ {
		d, err := u.Ask("Domain name (its DNS A/AAAA record must point at this machine)", domain, ValidDomain)
		if err != nil {
			return err
		}
		domain = strings.TrimSuffix(strings.ToLower(d), ".")
		if w.dnsPointsHere(ctx, domain) {
			break
		}
		if !u.Interactive {
			u.Warn("Continuing anyway; the certificate is requested once DNS is correct")
			break
		}
		cont, err := u.Confirm("Continue with "+domain+" anyway?", false)
		if err != nil {
			return err
		}
		if cont {
			break
		}
		domain = ""
	}
	a.Domain = domain
	email, err := u.Ask("Email for Let's Encrypt expiry notices (optional)", a.Email, validEmail)
	if err != nil {
		return err
	}
	a.Email = email
	return w.askRedirect()
}

func (w *Wizard) askRedirect() error {
	if !strings.HasSuffix(firstNonEmpty(w.answers.Listen, ":443"), ":443") {
		return nil
	}
	redirect, err := w.UI.Confirm("Also answer on port 80 and redirect http:// to https://?", false)
	if err != nil {
		return err
	}
	w.answers.RedirectHTTP = redirect
	return nil
}

// dnsPointsHere reports (and prints) whether domain resolves to this
// machine's public IP or one of its interface addresses.
func (w *Wizard) dnsPointsHere(ctx context.Context, domain string) bool {
	u := w.UI
	u.Dim("Checking DNS for %s (and this machine's public IP via %s)…", domain, firstNonEmpty(w.IPURL, IPifyURL))
	pub, ipErr := PublicIP(ctx, w.HTTP, w.IPURL)
	chk, err := CheckDNS(ctx, w.Resolve, domain, pub)
	if err != nil {
		u.Warn("%v", err)
		return false
	}
	if chk.Matched {
		u.OK("%s points at this machine (%s)", domain, joinIPs(chk.Addrs))
		return true
	}
	if ipErr != nil {
		u.Warn("%s resolves to %s; could not confirm it is this machine: %v", domain, joinIPs(chk.Addrs), ipErr)
	} else {
		u.Warn("%s resolves to %s, but this machine's public IP is %s", domain, joinIPs(chk.Addrs), pub)
	}
	u.Dim("Create an A record for %s pointing at this machine and allow TCP 443 in the firewall.", domain)
	return false
}

func (w *Wizard) accessSslip(ctx context.Context) error {
	u := w.UI
	a := &w.answers
	if d := strings.ToLower(w.Opt.Domain); d != "" {
		if !strings.HasSuffix(d, ".sslip.io") {
			return fmt.Errorf("--domain %q: sslip mode needs a name ending in .sslip.io (or leave it empty to detect)", d)
		}
		a.Domain = d
	} else {
		u.Info("sslip.io turns an IP into a hostname (203.0.113.7 → 203-0-113-7.sslip.io),")
		u.Info("so Relay can get a real certificate without you owning a domain.")
		detect, err := u.Confirm("Detect this machine's public IP via "+firstNonEmpty(w.IPURL, IPifyURL)+"?", true)
		if err != nil {
			return err
		}
		var ip net.IP
		if detect {
			ip, err = PublicIP(ctx, w.HTTP, w.IPURL)
			if err != nil {
				u.Warn("%v", err)
			}
		}
		if ip == nil {
			s, err := u.Ask("Public IP address of this machine", "", func(s string) error {
				_, err := SslipHost(net.ParseIP(strings.TrimSpace(s)))
				return err
			})
			if err != nil {
				return err
			}
			ip = net.ParseIP(strings.TrimSpace(s))
		}
		host, err := SslipHost(ip)
		if err != nil {
			return err
		}
		a.Domain = host
	}
	u.OK("Your address: https://%s", a.Domain)
	u.Dim("Port 443 must be reachable from the internet for the certificate.")
	return w.askRedirect()
}

func (w *Wizard) accessProxy() error {
	u := w.UI
	a := &w.answers
	pub, err := u.Ask("Public URL your proxy serves (optional, e.g. https://relay.example.com)", a.PublicURL, func(s string) error {
		if s == "" || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
			return nil
		}
		return errors.New("start the URL with https://")
	})
	if err != nil {
		return err
	}
	a.PublicURL = pub
	u.OK("Point your proxy at http://%s", firstNonEmpty(a.Listen, DefaultLocalListen))
	u.Dim("Forward WebSockets and the Host / X-Forwarded-* headers; TLS ends at the proxy.")
	return nil
}

func (w *Wizard) account() error {
	u := w.UI
	a := &w.answers
	user, err := u.Ask("Username", firstNonEmpty(w.Opt.User, w.Sys.Username()), ValidUser)
	if err != nil {
		return err
	}
	a.User = user
	pw, err := w.password()
	if err != nil {
		return err
	}
	hash, err := secret.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	a.PasswordHash = hash
	return nil
}

func (w *Wizard) password() (string, error) {
	u := w.UI
	if w.Opt.PasswordStdin {
		in := w.Opt.Stdin
		if in == nil {
			in = os.Stdin
		}
		line, err := bufio.NewReader(io.LimitReader(in, 4096)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		pw := strings.TrimRight(line, "\r\n")
		if err := CheckPassword(pw, w.answers.User); err != nil {
			return "", fmt.Errorf("password from stdin: %w", err)
		}
		u.OK("Password read from stdin")
		return pw, nil
	}
	idx, err := u.Choose("Password", []Choice{
		{Label: "Generate a strong one", Hint: "shown once at the end — save it in your password manager"},
		{Label: "Type my own", Hint: fmt.Sprintf("at least %d characters", MinPasswordLen)},
	}, 0)
	if err != nil {
		return "", err
	}
	if idx == 0 {
		pw, err := GeneratePassword()
		if err != nil {
			return "", err
		}
		w.generated = pw
		u.OK("Generated a password; it is shown at the end")
		return pw, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		pw, err := u.Secret("Password")
		if err != nil {
			return "", err
		}
		if err := CheckPassword(pw, w.answers.User); err != nil {
			u.Warn("%v", err)
			continue
		}
		again, err := u.Secret("Repeat it")
		if err != nil {
			return "", err
		}
		if again != pw {
			u.Warn("the passwords do not match")
			continue
		}
		u.OK("Password set")
		return pw, nil
	}
	return "", fmt.Errorf("%w: no valid password", ErrAborted)
}

func (w *Wizard) components(ctx context.Context) error {
	u := w.UI
	cat := w.Catalog
	if cat == nil {
		var err error
		if cat, err = toolbox.DefaultCatalog(); err != nil {
			return err
		}
	}
	prober := &toolbox.Prober{Finder: w.Finder, Timeout: 3 * time.Second, Concurrency: 4}
	goos := w.Sys.GOOS()
	var ids []string
	var agents []string
	if w.Opt.ComponentsSet {
		var err error
		if ids, err = ParseComponents(w.Opt.Components); err != nil {
			return err
		}
	} else if u.Interactive {
		opts := make([]Choice, 0, len(Components))
		avail := make([]Component, 0, len(Components))
		for _, c := range Components {
			rs, _, err := componentRecipes(cat, goos, []string{c.ID}, nil)
			if err != nil {
				return err
			}
			if len(rs) == 0 {
				continue // e.g. the desktop on macOS
			}
			hint := c.Hint
			if allInstalled(ctx, prober, rs) {
				hint += " (installed)"
			}
			avail = append(avail, c)
			opts = append(opts, Choice{Label: c.Label, Hint: hint})
		}
		sel, err := u.MultiChoose("Install anything now? (you can add more later from Toolbox)", opts, nil)
		if err != nil {
			return err
		}
		for i, s := range sel {
			if s {
				ids = append(ids, avail[i].ID)
			}
		}
		if contains(ids, "agents") {
			if agents, err = w.pickAgents(cat, goos); err != nil {
				return err
			}
		}
	}
	if len(ids) == 0 {
		u.Dim("Nothing to install. Toolbox in Relay installs tools any time.")
		return nil
	}
	rs, skipped, err := componentRecipes(cat, goos, ids, agents)
	if err != nil {
		return err
	}
	for _, s := range skipped {
		u.Dim("%s is not available on %s; skipped", s, goos)
	}
	if len(rs) == 0 {
		return nil
	}
	tmp := filepath.Join(w.Paths.CacheDir, "setup")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	inst := Installer{Sys: w.Sys, Catalog: cat, Prober: prober, TempDir: tmp}
	results := inst.Run(ctx, rs, func(r *toolbox.Recipe, i, n int) {
		sudo := ""
		if r.NeedsSudo(goos) {
			sudo = " (needs sudo)"
		}
		u.Printf("\n  %s %s%s\n", u.style(sAccent, fmt.Sprintf("→ %d/%d", i, n)), u.style(sBold, r.Name), u.style(sDim, sudo))
	})
	failed := 0
	for _, r := range results {
		switch {
		case r.Skipped:
			u.OK("%s is already installed", r.Recipe.Name)
		case r.Err != nil:
			failed++
			u.Fail("%v", r.Err)
		default:
			u.OK("%s installed", r.Recipe.Name)
		}
	}
	if failed > 0 {
		u.Warn("%d install(s) failed; setup continues. Retry them from Toolbox in Relay.", failed)
	}
	return ctx.Err()
}

func (w *Wizard) pickAgents(cat *toolbox.Catalog, goos string) ([]string, error) {
	var rs []*toolbox.Recipe
	for _, r := range cat.InCategory("agents") {
		if r.Supports(goos) {
			rs = append(rs, r)
		}
	}
	opts := make([]Choice, len(rs))
	defs := make([]bool, len(rs))
	var def []string
	for _, c := range Components {
		if c.ID == "agents" {
			def = c.Recipes
		}
	}
	for i, r := range rs {
		opts[i] = Choice{Label: r.Name, Hint: r.Description}
		defs[i] = contains(def, r.ID)
	}
	sel, err := w.UI.MultiChoose("Which agents?", opts, defs)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for i, s := range sel {
		if s {
			out = append(out, rs[i].ID)
		}
	}
	return out, nil
}

func allInstalled(ctx context.Context, p *toolbox.Prober, rs []*toolbox.Recipe) bool {
	for _, st := range p.ProbeAll(ctx, rs) {
		if !st.Installed {
			return false
		}
	}
	return true
}

func (w *Wizard) writeConfig() error {
	u := w.UI
	data, err := Render(w.answers, w.Now())
	if err != nil {
		return err
	}
	if err := w.Paths.Ensure(); err != nil {
		return fmt.Errorf("create Relay directories: %w", err)
	}
	bak, err := BackupConfig(w.Paths.ConfigFile, w.Now())
	if err != nil {
		return err
	}
	if bak != "" {
		u.Dim("Previous configuration saved as %s", bak)
	}
	cfg, err := WriteConfig(w.Paths, data)
	if err != nil {
		return err
	}
	w.cfg = cfg
	u.OK("Wrote %s (mode 0600)", w.Paths.ConfigFile)
	return nil
}

// binary returns the absolute, symlink-free path of the relay executable.
func (w *Wizard) binary() (string, error) {
	if w.Opt.Binary != "" {
		return filepath.Abs(w.Opt.Binary)
	}
	return Executable()
}

// Executable returns this program's resolved absolute path.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate relay binary: %w", err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

func (w *Wizard) services(ctx context.Context) ([]string, error) {
	u := w.UI
	if err := w.Paths.Ensure(); err != nil {
		return nil, fmt.Errorf("create Relay directories: %w", err)
	}
	bin, err := w.binary()
	if err != nil {
		return nil, err
	}
	goos := w.Sys.GOOS()
	if w.Opt.NoSystemd && w.Opt.UnitsDir == "" {
		u.Dim("Skipping services (--no-systemd). Start Relay yourself with:")
		u.Code(bin + " ptyd &")
		u.Code(bin + " serve")
		return nil, nil
	}
	dir := w.Opt.UnitsDir
	if dir == "" {
		dir = DefaultUnitsDir(goos, w.Paths.Home, w.Getenv)
	}
	data := UnitData{Binary: bin, Env: UnitEnv(w.Getenv), LogDir: filepath.Join(w.Paths.DataDir, "logs")}
	if goos == "darwin" {
		if err := os.MkdirAll(data.LogDir, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", data.LogDir, err)
		}
	}
	written, err := WriteUnits(goos, dir, data)
	if err != nil {
		return nil, err
	}
	u.OK("Wrote %s", strings.Join(written, ", "))
	if w.Opt.NoSystemd || w.Opt.NoStart {
		u.Dim("Not starting the services (%s).", map[bool]string{true: "--no-systemd", false: "--no-start"}[w.Opt.NoSystemd])
		return written, nil
	}
	mgr := Manager{Sys: w.Sys, UnitsDir: dir}
	if !mgr.Available(ctx) {
		u.Warn("The user service manager is not reachable (a container, or WSL without systemd).")
		u.Info("Start Relay yourself with:")
		u.Code(bin + " ptyd &")
		u.Code(bin + " serve")
		return written, nil
	}
	if goos == "linux" {
		w.linger(ctx)
		if err := w.bindCapability(ctx, bin); err != nil {
			return written, err
		}
	}
	if err := mgr.EnableNow(ctx); err != nil {
		return written, err
	}
	h, err := WaitHealthy(ctx, w.Paths.CtlSocket, w.HealthTimeout)
	if err != nil {
		u.Fail("Relay did not come up: %v", err)
		u.Info("See what happened with:")
		u.Code("relay logs")
		u.Code("relay doctor")
		return written, nil
	}
	w.started = true
	u.OK("Relay %s is running and starts automatically", h.Version)
	return written, nil
}

// linger makes user services survive logout and start at boot.
func (w *Wizard) linger(ctx context.Context) {
	u := w.UI
	on, err := Linger(ctx, w.Sys)
	if err == nil && on {
		u.OK("Services keep running after you log out (linger is on)")
		return
	}
	user := w.Sys.Username()
	if _, err := w.Sys.Output(ctx, "loginctl", "enable-linger", user); err == nil {
		u.OK("Enabled linger: Relay keeps running after you log out and starts at boot")
		return
	}
	u.Info("Without linger, systemd stops Relay when you log out. Enabling it needs root:")
	u.Code("sudo loginctl enable-linger " + user)
	if !u.Interactive {
		if _, err := w.Sys.Output(ctx, "sudo", "-n", "loginctl", "enable-linger", user); err == nil {
			u.OK("Enabled linger")
		}
		return
	}
	ok, err := u.Confirm("Run it now with sudo?", true)
	if err != nil || !ok {
		return
	}
	if err := w.Sys.Interactive(ctx, nil, "sudo", "loginctl", "enable-linger", user); err != nil {
		u.Warn("could not enable linger: %v", err)
		return
	}
	u.OK("Enabled linger")
}

// bindCapability lets a non-root relay bind ports below 1024 (:443).
func (w *Wizard) bindCapability(ctx context.Context, bin string) error {
	_, port, err := SplitListen(w.cfg.Server.Listen)
	if err != nil || port >= 1024 || w.Sys.Getuid() == 0 {
		return nil
	}
	u := w.UI
	if HasBindCap(ctx, w.Sys, bin) {
		u.OK("%s may bind port %d", bin, port)
		return nil
	}
	cmd := []string{"sudo", "setcap", "cap_net_bind_service=+ep", bin}
	u.Info("Port %d is privileged. Instead of running Relay as root, grant the binary exactly one", port)
	u.Info("capability — binding low ports — with:")
	u.Code(strings.Join(cmd, " "))
	u.Dim("It is dropped when the file changes; `relay update` asks to re-apply it.")
	var ok bool
	if u.Interactive {
		if ok, err = u.Confirm("Run it now?", true); err != nil {
			return err
		}
	} else if _, err := w.Sys.Output(ctx, "sudo", "-n", "true"); err == nil {
		ok = true
	}
	if !ok {
		u.Warn("Relay cannot bind :%d until you run the command above (then `systemctl --user restart relay`).", port)
		return nil
	}
	if err := w.Sys.Interactive(ctx, nil, cmd[0], cmd[1:]...); err != nil {
		u.Warn("setcap failed: %v", err)
		return nil
	}
	u.OK("Granted cap_net_bind_service to %s", bin)
	return nil
}

// HasBindCap reports whether bin carries cap_net_bind_service.
func HasBindCap(ctx context.Context, sys System, bin string) bool {
	getcap, err := sys.LookPath("getcap")
	if err != nil {
		for _, p := range []string{"/usr/sbin/getcap", "/sbin/getcap"} {
			if _, e := os.Stat(p); e == nil {
				getcap = p
			}
		}
	}
	if getcap == "" {
		return false
	}
	out, err := sys.Output(ctx, getcap, bin)
	return err == nil && strings.Contains(out, "cap_net_bind_service")
}

func (w *Wizard) revealPassword() {
	if w.generated == "" || w.cfg == nil {
		return
	}
	w.UI.Printf("\n  Your generated password (saved as a hash in relay.toml): %s\n", w.UI.style(sBold, w.generated))
	w.generated = ""
}

func (w *Wizard) final(res *Result) {
	u := w.UI
	user := w.answers.User
	if user == "" && w.cfg != nil {
		user = w.cfg.Auth.User
	}
	u.Printf("\n  %s\n\n", u.style(sBold, "Open Relay"))
	u.Printf("    %s\n", u.style(sAccent, res.URL))
	if user != "" {
		u.Printf("    user      %s\n", user)
	}
	if w.generated != "" {
		u.Printf("    password  %s\n", u.style(sBold, w.generated))
		u.Printf("    %s\n", u.style(sDim, "Shown only this once. Change it later in Settings."))
		w.generated = ""
	}
	if !IsLoopbackURL(res.URL) {
		if q, err := QR(res.URL, u.Color); err == nil {
			u.Printf("\n%s", q)
			u.Dim("Scan it with your phone, then Share → Add to Home Screen for a full-screen app.")
		}
	}
	u.Printf("\n")
	if !res.Started && res.Units == nil {
		u.Dim("Start it with `relay ptyd &` and `relay serve`.")
	}
	u.Dim("Check everything any time with `relay doctor`; `relay status` shows a summary.")
}

// IsLoopbackURL reports whether an http(s) URL points at this machine only.
func IsLoopbackURL(u string) bool {
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host := rest
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validEmail(s string) error {
	if s == "" {
		return nil
	}
	at := strings.Index(s, "@")
	if at < 1 || at == len(s)-1 || strings.ContainsAny(s, " \t<>") {
		return fmt.Errorf("%q is not an email address", s)
	}
	return nil
}

func joinIPs(ips []net.IP) string {
	s := make([]string, len(ips))
	for i, ip := range ips {
		s[i] = ip.String()
	}
	return strings.Join(s, ", ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
