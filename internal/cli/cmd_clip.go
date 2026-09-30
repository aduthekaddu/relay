package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// maxClipBytes mirrors clip.MaxBytes (the server enforces it too).
const maxClipBytes = 256 << 10

func init() {
	var paste, list bool
	var limit int
	Register(&Command{
		Name:    "clip",
		Group:   "Terminal",
		Summary: "Copy to / paste from the Relay clipboard shared by your devices",
		Usage:   "echo text | relay clip   ·   relay clip text…   ·   relay clip -p   ·   relay clip --list",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&paste, "p", false, "print the latest clip")
			fs.BoolVar(&paste, "paste", false, "same as -p")
			fs.BoolVar(&list, "list", false, "list recent clips")
			fs.IntVar(&limit, "n", 20, "number of clips for --list")
		},
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			switch {
			case paste:
				return clipPaste(ctx, os.Stdout)
			case list:
				return clipList(ctx, os.Stdout, limit)
			}
			text, err := clipInput(args, os.Stdin)
			if err != nil {
				return err
			}
			return CallLocal(ctx, "POST", "/api/v1/clip", map[string]string{"text": text, "source": "cli"}, nil)
		},
	})
}

func clipInput(args []string, stdin *os.File) (string, error) {
	text, err := messageFrom(args, stdin, maxClipBytes)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", &ExitError{Code: 2, Msg: "nothing to copy"}
	}
	return text, nil
}

func clipPaste(ctx context.Context, w io.Writer) error {
	var clips []api.Clip
	if err := CallLocal(ctx, "GET", "/api/v1/clip?limit=1", nil, &clips); err != nil {
		return err
	}
	if len(clips) == 0 {
		return &ExitError{Code: 1, Msg: "the clipboard is empty"}
	}
	_, err := io.WriteString(w, clips[0].Text)
	return err
}

func clipList(ctx context.Context, w io.Writer, limit int) error {
	if limit < 1 {
		limit = 1
	}
	var clips []api.Clip
	if err := CallLocal(ctx, "GET", fmt.Sprintf("/api/v1/clip?limit=%d", limit), nil, &clips); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range clips {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", ago(time.Since(c.At)), c.Source, humanBytes(int64(c.Size)), oneLine(c.Text, 60))
	}
	return tw.Flush()
}

// oneLine collapses whitespace and cuts s to n runes for listings.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
