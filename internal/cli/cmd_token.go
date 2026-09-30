package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func init() {
	Register(&Command{
		Name:    "token",
		Aliases: []string{"tokens"},
		Group:   "Account",
		Summary: "Create, list and revoke API tokens",
		Usage:   "relay token create <name> | relay token list [--json] | relay token revoke <id>",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("json", false, "machine-readable output")
		},
		Run: runToken,
	})
}

func runToken(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) == 0 {
		return &ExitError{Code: 2, Msg: "usage: relay token create <name> | list | revoke <id>"}
	}
	sub, rest := args[0], args[1:]
	// Allow flags after the subcommand too (relay token list --json).
	if err := fs.Parse(rest); err != nil {
		return &ExitError{Code: 2}
	}
	rest = fs.Args()
	asJSON := flagBool(fs, "json")

	acc, closeDB, err := openAccounts(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	actor, _ := acc.Username(ctx)

	switch sub {
	case "create", "new", "add":
		name := strings.TrimSpace(strings.Join(rest, " "))
		if name == "" {
			return &ExitError{Code: 2, Msg: "usage: relay token create <name>"}
		}
		t, err := acc.CreateToken(ctx, name)
		if err != nil {
			return userError(err)
		}
		_ = acc.Record(ctx, api.AuditEntry{Event: "token.create", Actor: actor, IP: "local", Detail: t.Name})
		if asJSON {
			return writeJSON(t)
		}
		fmt.Printf("Created token %q (%s).\n", t.Name, t.ID)
		fmt.Println("Copy it now; it won't be shown again:")
		fmt.Println()
		fmt.Println("  " + t.Token)
		fmt.Println()
		fmt.Println("Use it as: Authorization: Bearer <token>")
		return nil
	case "list", "ls":
		if len(rest) > 0 {
			return &ExitError{Code: 2, Msg: "usage: relay token list [--json]"}
		}
		ts, err := acc.ListTokens(ctx)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(ts)
		}
		if len(ts) == 0 {
			fmt.Println("No API tokens. Create one with: relay token create <name>")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tCREATED\tLAST USED")
		for _, t := range ts {
			fmt.Fprintf(tw, "%s\t%s\t%s…\t%s\t%s\n", t.ID, t.Name, t.Prefix, when(t.CreatedAt), when(t.LastUsedAt))
		}
		return tw.Flush()
	case "revoke", "rm", "delete":
		if len(rest) != 1 {
			return &ExitError{Code: 2, Msg: "usage: relay token revoke <id>"}
		}
		t, err := acc.RevokeToken(ctx, rest[0])
		if err != nil {
			return userError(err)
		}
		_ = acc.Record(ctx, api.AuditEntry{Event: "token.revoke", Actor: actor, IP: "local", Detail: t.Name})
		fmt.Printf("Revoked token %q (%s).\n", t.Name, t.ID)
		return nil
	default:
		return &ExitError{Code: 2, Msg: fmt.Sprintf("unknown token command %q (create, list, revoke)", sub)}
	}
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// when formats a timestamp for tables ("never" for zero).
func when(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}
