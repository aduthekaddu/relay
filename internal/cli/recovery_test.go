package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
)

func TestRecoveryStatePermissions(t *testing.T) {
	for _, tc := range []string{"private", "public-directory", "public-db", "readonly-db", "foreign-owner", "symlink", "hardlink", "public-wal", "missing"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0o700)
			db := filepath.Join(dir, "relay.db")
			if err := os.WriteFile(db, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := config.Paths{DataDir: dir, DB: db}
			switch tc {
			case "public-directory":
				os.Chmod(dir, 0o755)
			case "public-db":
				os.Chmod(db, 0o644)
			case "readonly-db":
				os.Chmod(db, 0o400)
			case "foreign-owner":
				info, err := os.Lstat(db)
				if err != nil {
					t.Fatal(err)
				}
				if privateRecoveryFile(info, false, os.Geteuid()+1) {
					t.Fatal("foreign UID accepted")
				}
				return
			case "symlink":
				os.Rename(db, db+"-target")
				os.Symlink(db+"-target", db)
			case "hardlink":
				os.Link(db, db+"-alias")
			case "public-wal":
				os.WriteFile(db+"-wal", nil, 0o644)
			case "missing":
				os.Remove(db)
			}
			err := checkRecoveryState(p)
			if (err == nil) != (tc == "private") {
				t.Fatalf("permission result mismatch: %v", err)
			}
		})
	}
}

func TestRecoveryCLIFlagsAndStaleSocket(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	t.Setenv("RELAY_SOCKET", filepath.Join(t.TempDir(), "stale.sock"))
	ctx := context.Background()
	a, closeDB, err := openAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := a.SetPassword(ctx, "fixture", "old synthetic password"); err != nil {
		t.Fatal(err)
	}
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CtlSocket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	var help strings.Builder
	printCommandHelp(&help, commands["passwd"])
	for _, name := range []string{"user", "stdin", "keep-sessions", "reset-totp"} {
		if !strings.Contains(help.String(), "-"+name) {
			t.Fatalf("missing help flag %s", name)
		}
	}
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	commands["passwd"].Flags(fs)
	fs.Parse([]string{"--reset-totp", "--keep-sessions", "--stdin"})
	if err := runPasswd(ctx, fs, nil); err == nil {
		t.Fatal("keep-sessions accepted with recovery")
	}
	fs.Set("keep-sessions", "false")
	// Keep a separate DB handle open, as a running server would. No socket is used.
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	input.WriteString("new synthetic password\n")
	input.Seek(0, io.SeekStart)
	saved := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = saved }()
	if err := runPasswd(ctx, fs, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p.CtlSocket)
	if err != nil || string(data) != "stale" {
		t.Fatal("recovery changed stale socket")
	}
	entries, err := a.Activity(ctx, 10)
	if err != nil || len(entries) != 1 || entries[0].Event != "totp.recover" {
		t.Fatal("missing recovery audit")
	}
}
