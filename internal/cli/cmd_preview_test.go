package cli

import (
	"bytes"
	"strings"
	"testing"

	"rsc.io/qr"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestParsePortArg(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"5173", 5173, true},
		{":3000", 3000, true},
		{"localhost:8080", 8080, true},
		{"http://127.0.0.1:4321/", 4321, true},
		{"0", 0, false},
		{"70000", 0, false},
		{"vite", 0, false},
	}
	for _, tt := range tests {
		got, err := parsePortArg(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parsePortArg(%q) = %d, %v", tt.in, got, err)
		}
	}
}

// TestRenderQRRoundTrip decodes the half-block drawing back into modules
// and compares it with the encoder's matrix.
func TestRenderQRRoundTrip(t *testing.T) {
	code, err := qr.Encode("https://5173.dev.example.com/", qr.M)
	if err != nil {
		t.Fatal(err)
	}
	for _, ansi := range []bool{false, true} {
		out := renderQR(code.Size, code.Black, ansi)
		if ansi {
			out = strings.NewReplacer("\x1b[30;47m", "", "\x1b[0m", "").Replace(out)
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		const quiet = 2
		if want := (code.Size + 2*quiet + 1) / 2; len(lines) != want {
			t.Fatalf("ansi=%v: %d lines, want %d", ansi, len(lines), want)
		}
		for row, line := range lines {
			cells := []rune(line)
			if len(cells) != code.Size+2*quiet {
				t.Fatalf("ansi=%v row %d width %d", ansi, row, len(cells))
			}
			for col, c := range cells {
				var top, bot bool // "dark" modules
				switch c {
				case '█':
					top, bot = true, true
				case '▀':
					top = true
				case '▄':
					bot = true
				}
				if !ansi { // plain mode draws light modules
					top, bot = !top, !bot
				}
				x, y := col-quiet, row*2-quiet
				want := func(x, y int) bool {
					return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
				}
				if top != want(x, y) || bot != want(x, y+1) {
					t.Fatalf("ansi=%v mismatch at module (%d,%d)", ansi, x, y)
				}
			}
		}
	}
}

func TestPrintPreview(t *testing.T) {
	var out, errOut bytes.Buffer
	link := api.PreviewLink{Port: 5173, URL: "https://5173.dev.example.com/", Mode: "subdomain", Listening: true,
		Preview: &api.Preview{Port: 5173, Label: "Vite", Title: "Demo"}}
	if err := printPreview(&out, &errOut, link, false, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Vite on :5173 — Demo\nhttps://5173.dev.example.com/\n" || errOut.Len() != 0 {
		t.Errorf("out = %q err = %q", out.String(), errOut.String())
	}
	out.Reset()
	link.Listening, link.Preview = false, nil
	if err := printPreview(&out, &errOut, link, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "Nothing is listening") || !strings.Contains(out.String(), "█") {
		t.Errorf("idle output = %q / %q", out.String(), errOut.String())
	}
	if err := printPreview(&out, &errOut, api.PreviewLink{Port: 1}, false, false); err == nil {
		t.Error("empty URL accepted")
	}
}
