package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/aduthekaddu/relay/internal/api"
)

func init() {
	Register(&Command{
		Name:    "open",
		Group:   "Terminal",
		Summary: "Open a file or folder in Relay on the device you are using",
		Usage:   "relay open <path[:line[:col]]>",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("q", false, "print nothing on success")
		},
		Run: runOpen,
	})
}

// lineSuffix matches "file.go:42" and "file.go:42:7" (column ignored).
var lineSuffix = regexp.MustCompile(`^(.+?):(\d+)(?::\d+)?$`)

// parseOpenTarget splits "path[:line[:col]]" and makes path absolute
// relative to cwd. A path that exists verbatim (even with a colon in its
// name) wins over the line syntax.
func parseOpenTarget(arg, cwd string, exists func(string) bool) (string, int) {
	abs := func(p string) string {
		if filepath.IsAbs(p) || p == "~" || len(p) > 1 && p[:2] == "~/" {
			return filepath.Clean(p)
		}
		return filepath.Join(cwd, p)
	}
	if exists(abs(arg)) {
		return abs(arg), 0
	}
	if m := lineSuffix.FindStringSubmatch(arg); m != nil {
		if n, err := strconv.Atoi(m[2]); err == nil {
			return abs(m[1]), n
		}
	}
	return abs(arg), 0
}

func runOpen(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) != 1 {
		fs.Usage()
		return &ExitError{Code: 2}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	p, line := parseOpenTarget(args[0], cwd, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	var out api.OpenRequest
	if err := CallLocal(ctx, "POST", "/api/v1/open", api.OpenRequest{Path: p, Line: line}, &out); err != nil {
		return err
	}
	if quiet := fs.Lookup("q"); quiet == nil || quiet.Value.String() != "true" {
		if out.Line > 0 {
			fmt.Fprintf(os.Stderr, "Opening %s:%d in Relay\n", out.Path, out.Line)
		} else {
			fmt.Fprintf(os.Stderr, "Opening %s in Relay\n", out.Path)
		}
	}
	return nil
}
