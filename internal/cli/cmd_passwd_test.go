package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestReadPasswordLine(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"correct horse battery\n", "correct horse battery", false},
		{"windows line ending\r\n", "windows line ending", false},
		{"no newline at eof", "no newline at eof", false},
		{"  keeps spaces  \nsecond line", "  keeps spaces  ", false},
		{"", "", true},
		{"\n", "", true},
	}
	for _, tc := range cases {
		got, err := readPasswordLine(strings.NewReader(tc.in))
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("readPasswordLine(%q) = %q, %v", tc.in, got, err)
		}
		var ee *ExitError
		if tc.wantErr && !errors.As(err, &ee) {
			t.Errorf("want *ExitError, got %T", err)
		}
	}
}
