package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"rsc.io/qr"

	"github.com/aduthekaddu/relay/internal/api"
)

// cmd_preview.go is owned by the previews feature.

func init() {
	var noQR bool
	Register(&Command{
		Name:    "preview",
		Group:   "Terminal",
		Summary: "Print the preview URL (and a QR code) for a local port",
		Usage:   "relay preview [--no-qr] <port>",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&noQR, "no-qr", false, "print only the URL")
		},
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			if len(args) != 1 {
				return &ExitError{Code: 2, Msg: "usage: relay preview <port>"}
			}
			port, err := parsePortArg(args[0])
			if err != nil {
				return &ExitError{Code: 2, Msg: err.Error()}
			}
			var link api.PreviewLink
			if err := CallLocal(ctx, "GET", "/api/v1/previews/"+strconv.Itoa(port)+"/link", nil, &link); err != nil {
				return err
			}
			tty := isTTY(os.Stdout)
			return printPreview(os.Stdout, os.Stderr, link, !noQR, tty && os.Getenv("NO_COLOR") == "")
		},
	})
}

// parsePortArg accepts "5173", ":5173", "localhost:5173" or a loopback URL.
func parsePortArg(s string) (int, error) {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Port() != "" && u.Host != "" {
		s = u.Port()
	} else if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return p, nil
}

func printPreview(out, errOut io.Writer, link api.PreviewLink, withQR, ansi bool) error {
	if link.URL == "" {
		return errors.New("the server returned no preview URL")
	}
	if link.Listening && link.Preview != nil {
		name := link.Preview.Label
		if name == "" {
			name = link.Preview.Process
		}
		if name == "" {
			name = "Preview"
		}
		line := fmt.Sprintf("%s on :%d", name, link.Port)
		if link.Preview.Title != "" {
			line += " — " + link.Preview.Title
		}
		fmt.Fprintln(out, line)
	} else {
		fmt.Fprintf(errOut, "Nothing is listening on :%d yet; the link works as soon as it is.\n", link.Port)
	}
	fmt.Fprintln(out, link.URL)
	if !withQR {
		return nil
	}
	code, err := qr.Encode(link.URL, qr.M)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	fmt.Fprintln(out)
	_, err = io.WriteString(out, renderQR(code.Size, code.Black, ansi))
	return err
}

// renderQR draws a QR matrix with Unicode half blocks: each character
// cell holds two vertical modules, so the code stays square in a
// terminal. With ansi, colours are explicit (black on white) so it scans
// on any theme; without, light modules are drawn filled, which suits the
// usual light-on-dark terminal.
func renderQR(size int, black func(x, y int) bool, ansi bool) string {
	const quiet = 2
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		return black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < size+quiet; y += 2 {
		if ansi {
			b.WriteString("\x1b[30;47m") // black foreground, white background
		}
		for x := -quiet; x < size+quiet; x++ {
			top, bot := dark(x, y), dark(x, y+1)
			if ansi {
				// Foreground draws the top half (▀), background shows below.
				switch {
				case top && bot:
					b.WriteString("█")
				case top:
					b.WriteString("▀")
				case bot:
					b.WriteString("▄")
				default:
					b.WriteString(" ")
				}
				continue
			}
			switch {
			case !top && !bot:
				b.WriteString("█")
			case !top:
				b.WriteString("▀")
			case !bot:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if ansi {
			b.WriteString("\x1b[0m")
		}
		b.WriteByte('\n')
	}
	return b.String()
}
