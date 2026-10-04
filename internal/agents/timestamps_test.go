package agents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Synthetic adapter inputs exercise the existing parser's unknown-time policy;
// this does not establish behavior of an installed provider or native transcript.
func TestParsedMessageTimestampSerialization(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input any
		known bool
	}{
		{"missing", nil, false}, {"null", json.RawMessage("null"), false},
		{"invalid", "invalid", false}, {"zero", "0001-01-01T00:00:00Z", false},
		{"defined-offset", "2026-03-12T17:30:00.123456789+05:30", true},
		{"future", "2099-01-01T00:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := parseTime(tc.input)
			msg := api.AgentMessage{ID: "fixture", Role: "assistant", At: at}
			data, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err != nil {
				t.Fatal(err)
			}
			if _, got := obj["at"]; got != tc.known {
				t.Fatalf("known = %v, want %v: %s", got, tc.known, data)
			}
			if tc.known {
				want, err := time.Parse(time.RFC3339Nano, tc.input.(string))
				if err != nil {
					t.Fatal(err)
				}
				var decoded api.AgentMessage
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatal(err)
				}
				if !decoded.At.Equal(want) || decoded.At.Nanosecond() != want.Nanosecond() {
					t.Fatalf("instant/precision changed: %s", data)
				}
			}
		})
	}
}
