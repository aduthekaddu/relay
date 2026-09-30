package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func init() {
	Register(&Command{
		Name:    "ls",
		Group:   "Terminal",
		Summary: "List terminal sessions",
		Usage:   "relay ls [--json] [--live]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("json", false, "print JSON")
			fs.Bool("live", false, "hide exited sessions")
		},
		Run: runLs,
	})
}

func runLs(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	c, err := ptydClient(ctx, false)
	if err != nil {
		return err
	}
	list, err := c.List(ctx)
	if err != nil {
		return friendly(err)
	}
	if fs.Lookup("live").Value.String() == "true" {
		live := list[:0]
		for _, t := range list {
			if t.Activity != api.ActivityExited {
				live = append(live, t)
			}
		}
		list = live
	}
	if list == nil {
		list = []api.TerminalSession{}
	}
	if fs.Lookup("json").Value.String() == "true" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	home, _ := os.UserHomeDir()
	printSessions(os.Stdout, list, home, time.Now())
	return nil
}

// printSessions renders the `relay ls` table.
func printSessions(w io.Writer, list []api.TerminalSession, home string, now time.Time) {
	if len(list) == 0 {
		fmt.Fprintln(w, "No sessions. Start one with `relay run -- <command>` or from the web app.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCWD\tCLIENTS\tAGE")
	for _, t := range list {
		cwd := t.CurrentCwd
		if cwd == "" {
			cwd = t.Cwd
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			t.ID, clean(ellipsis(t.Name, 32)), status(&t), clean(ellipsis(tildePath(cwd, home), 40)), t.Clients, age(now.Sub(t.CreatedAt)))
	}
	tw.Flush()
}

// status is the STATUS column: activity, attention or exit code.
func status(t *api.TerminalSession) string {
	switch {
	case t.Activity == api.ActivityExited && t.ExitCode != nil:
		return "exited(" + strconv.Itoa(*t.ExitCode) + ")"
	case t.Activity == api.ActivityExited:
		return "exited"
	case t.Attention != nil:
		return "waiting"
	case t.Activity == "":
		return "live"
	}
	return string(t.Activity)
}

func tildePath(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

// clean removes control characters (session names come from OSC titles).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

func ellipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// age formats a duration compactly: 42s, 5m, 3h, 2d.
func age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}
