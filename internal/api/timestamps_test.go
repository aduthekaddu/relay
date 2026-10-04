package api_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Full serialized objects are shared with the browser tests. In addition to
// timestamps they pin nil arrays, lifecycle states and a present exit code 0.
func TestOptionalTimestampResponses(t *testing.T) {
	types := map[string]reflect.Type{
		"Passkey": reflect.TypeFor[api.Passkey](), "APIToken": reflect.TypeFor[api.APIToken](),
		"TerminalSession": reflect.TypeFor[api.TerminalSession](), "AgentMessage": reflect.TypeFor[api.AgentMessage](),
		"QuotaWindow": reflect.TypeFor[api.QuotaWindow](), "Workspace": reflect.TypeFor[api.Workspace](),
		"Service": reflect.TypeFor[api.Service](), "App": reflect.TypeFor[api.App](),
		"Schedule": reflect.TypeFor[api.Schedule](), "ScheduleRun": reflect.TypeFor[api.ScheduleRun](),
		"SearchResult": reflect.TypeFor[api.SearchResult](), "FileJob": reflect.TypeFor[api.FileJob](),
	}
	var fixtures []struct {
		Name, Type, Field string
		Absent, Defined   map[string]json.RawMessage
	}
	data, err := os.ReadFile("testdata/timestamps.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 14 {
		t.Fatalf("inventoried fields = %d, want 14", len(fixtures))
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			typ, ok := types[f.Type]
			if !ok {
				t.Fatalf("unknown fixture type %q", f.Type)
			}
			found := false
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.Tag.Get("json") == f.Field+",omitempty,omitzero" && field.Type == reflect.TypeFor[time.Time]() {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s lacks inventoried optional time tag", f.Field)
			}
			for name, value := range map[string]json.RawMessage{
				"missing": nil, "null": []byte("null"), "year-one": []byte(`"0001-01-01T00:00:00Z"`),
				"equivalent-zero-offset": []byte(`"0001-01-01T01:00:00.000000000+01:00"`),
			} {
				t.Run(name, func(t *testing.T) {
					input := make(map[string]json.RawMessage)
					for k, v := range f.Absent {
						input[k] = v
					}
					if value != nil {
						input[f.Field] = value
					}
					assertTimestampResponse(t, reflect.New(typ).Interface(), input, f.Absent)
				})
			}
			t.Run("defined-offset-nanoseconds", func(t *testing.T) {
				assertTimestampResponse(t, reflect.New(typ).Interface(), f.Defined, f.Defined)
			})
			for _, stamp := range []string{"2026-03-12T12:00:00Z", "2099-01-01T00:00:00.000000001-04:00", "0001-01-01T00:00:00.000000001Z"} {
				t.Run(stamp, func(t *testing.T) {
					input := make(map[string]json.RawMessage)
					for k, v := range f.Absent {
						input[k] = v
					}
					input[f.Field], _ = json.Marshal(stamp)
					assertTimestampResponse(t, reflect.New(typ).Interface(), input, input)
				})
			}
			for _, invalid := range []string{"", "invalid", "2026-02-30T00:00:00Z", "2026-03-12T24:00:00Z"} {
				input, _ := json.Marshal(map[string]string{f.Field: invalid})
				if err := json.Unmarshal(input, reflect.New(typ).Interface()); err == nil {
					t.Errorf("accepted invalid timestamp %q", invalid)
				}
			}
		})
	}
}

func assertTimestampResponse(t *testing.T, target any, input, want map[string]json.RawMessage) {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(encoded, &gotValue); err != nil {
		t.Fatal(err)
	}
	encodedWant, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encodedWant, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("response\n%s\nwant\n%s", encoded, encodedWant)
	}
}

func TestTimestampRepresentationControls(t *testing.T) {
	for _, tc := range []struct {
		name            string
		value           any
		present, absent []string
	}{
		{"partial-agent", api.AgentSession{ID: "fixture-agent", Status: api.AgentHistory}, []string{"startedAt", "updatedAt"}, nil},
		{"partial-terminal", api.TerminalSession{ID: "fixture-terminal"}, []string{"createdAt", "command"}, []string{"lastOutputAt", "lastInputAt", "exitedAt"}},
		{"unused-created-token", api.CreatedToken{Token: "synthetic-token"}, []string{"createdAt", "token", "prefix"}, []string{"lastUsedAt"}},
		{"unused-passkey-detail", api.PasskeyDetail{Synced: true, AAGUID: "fixture"}, []string{"createdAt", "synced", "aaguid"}, []string{"lastUsedAt"}},
		{"nil-pointer", api.PreviewCapability{}, []string{"configuredMode"}, []string{"checkedAt"}},
		{"empty-slice", api.CronPreview{Valid: true}, []string{"valid"}, []string{"next"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(b, &obj); err != nil {
				t.Fatal(err)
			}
			for _, key := range tc.present {
				if _, ok := obj[key]; !ok {
					t.Errorf("missing %s in %s", key, b)
				}
			}
			for _, key := range tc.absent {
				if _, ok := obj[key]; ok {
					t.Errorf("unexpected %s in %s", key, b)
				}
			}
		})
	}
	// Only the inventoried optional fields omit zero; required partial-session
	// timestamps retain their wire contract and are guarded at browser display.
	b, err := json.Marshal(api.AgentSession{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "0001-01-01T00:00:00Z") != 2 {
		t.Fatalf("required timestamps changed: %s", b)
	}
	stamp, err := time.Parse(time.RFC3339Nano, "2026-11-01T01:30:00.123456789-04:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{api.PreviewCapability{CheckedAt: &stamp}, api.CronPreview{Next: []time.Time{stamp}}} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "2026-11-01T01:30:00.123456789-04:00") {
			t.Fatalf("existing optional representation changed: %s", b)
		}
	}
}
