package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/aduthekaddu/relay/internal/api"
)

// maxNotifyStdin caps a message piped into `relay notify`.
const maxNotifyStdin = 64 << 10

func init() {
	var title, kind, link, severity string
	Register(&Command{
		Name:    "notify",
		Group:   "Terminal",
		Summary: "Send a notification to your devices",
		Usage:   "relay notify [-t title] [--kind done] [--link /path] message…   (message from stdin when omitted)",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&title, "t", "", "notification title (default: first line of the message)")
			fs.StringVar(&title, "title", "", "same as -t")
			fs.StringVar(&kind, "kind", "custom", "attention | done | exited | system | schedule | custom")
			fs.StringVar(&link, "link", "", "in-app path to open when tapped (default: this terminal)")
			fs.StringVar(&severity, "severity", "", "info | success | warning | danger (default: by kind)")
		},
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			msg, err := messageFrom(args, os.Stdin, maxNotifyStdin)
			if err != nil {
				return err
			}
			req := buildNotifyRequest(title, msg, kind, link, severity, os.Getenv("RELAY_SESSION"))
			if req.Title == "" {
				return &ExitError{Code: 2, Msg: "nothing to send: give a message or pipe one on stdin"}
			}
			return CallLocal(ctx, "POST", "/api/v1/notify", req, nil)
		},
	})
}

// buildNotifyRequest maps CLI input to the API request. Without -t the
// first line of the message becomes the title and the rest the body.
func buildNotifyRequest(title, msg, kind, link, severity, session string) api.NotifyRequest {
	title = strings.TrimSpace(title)
	msg = strings.TrimSpace(msg)
	if title == "" {
		first, rest, _ := strings.Cut(msg, "\n")
		title, msg = strings.TrimSpace(first), strings.TrimSpace(rest)
	}
	return api.NotifyRequest{
		Kind:      kind,
		Title:     title,
		Body:      msg,
		Link:      link,
		Severity:  severity,
		SessionID: session,
	}
}

// messageFrom joins args, or reads stdin when there are none and stdin is
// not a terminal. Input larger than limit is an error, not a truncation.
func messageFrom(args []string, stdin *os.File, limit int64) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if stdin == nil || term.IsTerminal(int(stdin.Fd())) {
		return "", &ExitError{Code: 2, Msg: "no message: pass it as arguments or pipe it on stdin"}
	}
	return readLimited(stdin, limit)
}

func readLimited(r io.Reader, limit int64) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	if int64(len(b)) > limit {
		return "", errors.New("input too large (max " + humanBytes(limit) + ")")
	}
	return string(b), nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
