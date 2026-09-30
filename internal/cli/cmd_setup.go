package cli

import (
	"context"
	"errors"
	"flag"
	"os"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
)

func init() {
	Register(&Command{
		Name:    "setup",
		Group:   "Setup",
		Summary: "Configure access, your account, optional tools and background services",
		Usage:   "relay setup [--access tailscale|domain|sslip|proxy|local] [--domain d] [--user u --password-stdin] [--components desktop,code,agents,tools] [--yes]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("access", "", "how you reach this machine: tailscale, domain, sslip, proxy or local")
			fs.String("domain", "", "public domain name (domain mode) or <ip>.sslip.io name (sslip mode)")
			fs.String("email", "", "contact email for Let's Encrypt (optional)")
			fs.String("listen", "", "listen address override, e.g. :443 or 127.0.0.1:7777")
			fs.String("public-url", "", "external URL when behind Tailscale or a proxy, e.g. https://relay.example.com")
			fs.String("user", "", "login username (default: your system user)")
			fs.Bool("password-stdin", false, "read the password from the first line of stdin")
			fs.String("components", "", "optional installs: desktop,code,agents,tools, all or none")
			fs.Bool("yes", false, "non-interactive: accept defaults, never prompt")
			fs.Bool("force", false, "replace an existing relay.toml (a backup is kept)")
			fs.Bool("no-systemd", false, "do not install or start systemd/launchd services")
			fs.Bool("no-start", false, "install the services but do not start them")
			fs.String("units-dir", "", "write service units here instead of the default user unit directory")
		},
		Run: runSetup,
	})
}

func runSetup(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) > 0 {
		return &ExitError{Code: 2, Msg: "setup takes no arguments (see relay help setup)"}
	}
	str := func(n string) string { return fs.Lookup(n).Value.String() }
	flagBool := func(n string) bool { return str(n) == "true" }
	componentsSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "components" {
			componentsSet = true
		}
	})
	opt := setup.Options{
		Access:        str("access"),
		Domain:        str("domain"),
		Email:         str("email"),
		Listen:        str("listen"),
		PublicURL:     str("public-url"),
		User:          str("user"),
		PasswordStdin: flagBool("password-stdin"),
		Stdin:         os.Stdin,
		Components:    str("components"),
		ComponentsSet: componentsSet,
		Yes:           flagBool("yes"),
		Force:         flagBool("force"),
		NoSystemd:     flagBool("no-systemd"),
		NoStart:       flagBool("no-start"),
		UnitsDir:      str("units-dir"),
	}
	if opt.Components != "" {
		if _, err := setup.ParseComponents(opt.Components); err != nil {
			return &ExitError{Code: 2, Msg: err.Error()}
		}
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	// --password-stdin consumes stdin, so prompts must come from the TTY.
	ui, closeUI := setup.NewUI(!opt.Yes, os.Stdout)
	defer closeUI()
	w := &setup.Wizard{Opt: opt, UI: ui, Paths: paths}
	if _, err := w.Run(ctx); err != nil {
		if errors.Is(err, setup.ErrAborted) || errors.Is(err, context.Canceled) {
			return &ExitError{Code: 130, Msg: "setup cancelled; nothing after the last ✓ was changed"}
		}
		return err
	}
	return nil
}
