package cli

import "testing"

func TestParseOpenTarget(t *testing.T) {
	existing := map[string]bool{"/work/odd:name": true, "/work/a.go": true}
	exists := func(p string) bool { return existing[p] }
	for _, tc := range []struct {
		arg  string
		path string
		line int
	}{
		{"a.go", "/work/a.go", 0},
		{"a.go:42", "/work/a.go", 42},
		{"src/app.ts:10:5", "/work/src/app.ts", 10},
		{"/abs/file.txt:7", "/abs/file.txt", 7},
		{"odd:name", "/work/odd:name", 0},
		{"~/notes.md:3", "~/notes.md", 3},
		{"../up.txt", "/up.txt", 0},
		{"weird:", "/work/weird:", 0},
	} {
		p, line := parseOpenTarget(tc.arg, "/work", exists)
		if p != tc.path || line != tc.line {
			t.Errorf("parseOpenTarget(%q) = %q,%d; want %q,%d", tc.arg, p, line, tc.path, tc.line)
		}
	}
}
