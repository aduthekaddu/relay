package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/auth"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/store"
)

func init() {
	Register(&Command{
		Name:    "passwd",
		Group:   "Account",
		Summary: "Set or reset the sign-in password",
		Usage:   "relay passwd [--user name] [--stdin] [--keep-sessions] [--reset-totp]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("user", "", "username (creates the account if there is none; renames it otherwise)")
			fs.Bool("stdin", false, "read the password from standard input (one line) instead of prompting")
			fs.Bool("keep-sessions", false, "do not sign out existing browser sessions (incompatible with --reset-totp)")
			fs.Bool("reset-totp", false, "owner-only recovery: replace password, clear TOTP and sign out all browsers; keep tokens and passkeys")
		},
		Run: runPasswd,
	})
}

func runPasswd(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) > 0 {
		return &ExitError{Code: 2, Msg: "passwd takes no arguments (see 'relay help passwd')"}
	}
	if flagBool(fs, "reset-totp") {
		return runTOTPRecovery(ctx, fs)
	}
	acc, closeDB, err := openAccounts(ctx)
	if err != nil {
		return err
	}
	defer closeDB()

	username := strings.TrimSpace(flagString(fs, "user"))
	existing, err := acc.Username(ctx)
	if err != nil {
		return fmt.Errorf("read account: %w", err)
	}
	if username == "" && existing == "" {
		username = defaultUsername()
	}
	shown := username
	if shown == "" {
		shown = existing
	}
	if err := auth.ValidateUsername(shown); err != nil {
		return userError(err)
	}

	var password string
	if flagBool(fs, "stdin") {
		password, err = readPasswordLine(os.Stdin)
	} else {
		password, err = promptNewPassword(shown)
	}
	if err != nil {
		return err
	}
	if err := auth.CheckPassword(shown, password); err != nil {
		return userError(err)
	}

	created, err := acc.SetPassword(ctx, username, password)
	if err != nil {
		return userError(err)
	}
	event, msg := "password.change", "Password updated for "+shown+"."
	if created {
		event, msg = "account.create", "Account "+shown+" created."
	}
	_ = acc.Record(ctx, api.AuditEntry{Event: event, Actor: shown, IP: "local", Detail: "relay passwd"})

	if !created && !flagBool(fs, "keep-sessions") {
		ids, err := acc.RevokeAllSessions(ctx, "")
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			_ = acc.Record(ctx, api.AuditEntry{Event: "session.revoke", Actor: shown, IP: "local", Detail: fmt.Sprintf("%d session(s) signed out by relay passwd", len(ids))})
			msg += fmt.Sprintf(" Signed out %d browser session(s).", len(ids))
		}
	}
	fmt.Println(msg)
	return nil
}

// openAccounts opens the Relay database directly (works with or without a
// running server; SQLite WAL + busy timeout handle the concurrency).
func openAccounts(ctx context.Context) (*auth.Accounts, func(), error) {
	p, err := config.ResolvePaths()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve paths: %w", err)
	}
	if err := p.Ensure(); err != nil {
		return nil, nil, fmt.Errorf("prepare %s: %w", p.DataDir, err)
	}
	st, err := store.Open(p.DB)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", p.DB, err)
	}
	acc, err := auth.OpenAccounts(ctx, st)
	if err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("prepare account tables: %w", err)
	}
	return acc, func() { st.Close() }, nil
}

// defaultUsername is the name for a brand-new account: auth.user from
// relay.toml, else the OS user, else "admin".
func defaultUsername() string {
	if p, err := config.ResolvePaths(); err == nil {
		if cfg, err := config.Load(p); err == nil && strings.TrimSpace(cfg.Auth.User) != "" {
			return strings.TrimSpace(cfg.Auth.User)
		}
	}
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" && auth.ValidateUsername(u) == nil {
		return u
	}
	return "admin"
}

// promptNewPassword asks twice on the terminal without echo.
func promptNewPassword(user string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", &ExitError{Code: 2, Msg: "standard input is not a terminal; use --stdin to pipe the password"}
	}
	fmt.Fprintf(os.Stderr, "New password for %s (at least %d characters): ", user, auth.MinPasswordLen)
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if err := auth.CheckPassword(user, string(first)); err != nil {
		return "", userError(err)
	}
	fmt.Fprint(os.Stderr, "Type it again: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(first) != string(second) {
		return "", &ExitError{Code: 1, Msg: "the passwords don't match; nothing changed"}
	}
	return string(first), nil
}

// readPasswordLine reads one line (without its line ending) from r.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReaderSize(r, 4096).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", &ExitError{Code: 2, Msg: "no password on standard input"}
	}
	return line, nil
}

// userError turns validation errors into plain CLI messages.
func userError(err error) error {
	var he *httpx.Err
	if errors.As(err, &he) {
		return &ExitError{Code: 1, Msg: he.Message}
	}
	if errors.Is(err, auth.ErrUserExists) {
		return &ExitError{Code: 1, Msg: err.Error()}
	}
	return err
}

func flagString(fs *flag.FlagSet, name string) string {
	if f := fs.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func flagBool(fs *flag.FlagSet, name string) bool { return flagString(fs, name) == "true" }
