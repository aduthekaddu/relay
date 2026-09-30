package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const fixListUnits = `relay.service                 loaded    active   running Relay workspace
relay-ptyd.service            loaded    active   running Relay terminal daemon
● broken.service              loaded    failed   failed  Broken thing
dbus.service                  loaded    active   running D-Bus User Message Bus
idle.service                  loaded    inactive dead    Idle Thing
ghost.service                 not-found inactive dead    ghost.service
paths.target                  loaded    active   active  Paths
`

const fixShow = `Id=relay.service
Description=Relay workspace
LoadState=loaded
ActiveState=active
SubState=running
NRestarts=2
ActiveEnterTimestampMonotonic=5000000
InactiveEnterTimestampMonotonic=0

Id=broken.service
Description=Broken thing
LoadState=loaded
ActiveState=failed
SubState=failed
NRestarts=7
ActiveEnterTimestampMonotonic=1000000
InactiveEnterTimestampMonotonic=9000000

Id=nope.service
Description=nope.service
LoadState=not-found
ActiveState=inactive
SubState=dead
NRestarts=0
ActiveEnterTimestampMonotonic=0
InactiveEnterTimestampMonotonic=0
`

func TestParseListUnits(t *testing.T) {
	got := parseListUnits([]byte(fixListUnits))
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name
	}
	if strings.Join(names, ",") != "relay.service,relay-ptyd.service,broken.service,dbus.service,idle.service" {
		t.Fatalf("names = %v", names)
	}
	if got[2].Active != "failed" || got[2].Description != "Broken thing" || got[2].Managed {
		t.Errorf("broken = %+v", got[2])
	}
	if !got[0].Managed || !got[0].User || got[0].Sub != "running" {
		t.Errorf("relay = %+v", got[0])
	}
}

func TestParseShow(t *testing.T) {
	boot := time.Unix(1700000000, 0)
	got := parseShow([]byte(fixShow), boot)
	r := got["relay.service"]
	if r.Restarts != 2 || !r.Since.Equal(boot.Add(5*time.Second)) || !r.Managed || r.Active != "active" {
		t.Errorf("relay = %+v", r)
	}
	// Inactive/failed units report when they stopped.
	if b := got["broken.service"]; !b.Since.Equal(boot.Add(9*time.Second)) || b.Restarts != 7 {
		t.Errorf("broken = %+v", b)
	}
	if got["nope.service"].load != "not-found" {
		t.Errorf("nope = %+v", got["nope.service"])
	}
	if s := parseShow([]byte(fixShow), time.Time{})["relay.service"]; !s.Since.IsZero() {
		t.Errorf("zero boot must leave Since unset: %v", s.Since)
	}
}

func TestNormalizeUnit(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"relay", "relay.service", true},
		{"relay.service", "relay.service", true},
		{"app@inst.service", "app@inst.service", true},
		{"x-y_z:1.service", "x-y_z:1.service", true},
		{"-relay", "", false},
		{"--now.service", "", false},
		{"relay.timer", "", false},
		{"../etc.service", "", false},
		{"a b.service", "", false},
		{"a;b", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, err := NormalizeUnit(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("NormalizeUnit(%q) = %q, %v", tt.in, got, err)
		}
	}
}

// fakeSystemctl records argv and answers from a table.
type fakeSystemctl struct {
	calls [][]string
	fail  map[string]error
}

func (f *fakeSystemctl) run(ctx context.Context, argv ...string) ([]byte, error) {
	f.calls = append(f.calls, argv)
	verb := argv[3]
	if err := f.fail[verb]; err != nil {
		return nil, err
	}
	switch verb {
	case "list-units":
		return []byte(fixListUnits), nil
	case "show":
		return []byte(fixShow), nil
	}
	return nil, nil
}

func newFakeUnits() (*Units, *fakeSystemctl) {
	f := &fakeSystemctl{fail: map[string]error{}}
	u := &Units{Systemctl: "/usr/bin/systemctl", Run: f.run, BootTime: func() time.Time { return time.Unix(1700000000, 0) }}
	return u, f
}

func TestUnitsListSortedAndCached(t *testing.T) {
	u, f := newFakeUnits()
	list, err := u.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, len(list))
	for i, s := range list {
		order[i] = s.Name
	}
	// Relay first, then failed, then active, then the rest.
	if strings.Join(order, ",") != "relay-ptyd.service,relay.service,broken.service,dbus.service,idle.service" {
		t.Fatalf("order = %v", order)
	}
	if list[1].Restarts != 2 || list[1].Since.IsZero() {
		t.Errorf("details not merged: %+v", list[1])
	}
	n := len(f.calls)
	if _, err := u.List(context.Background()); err != nil || len(f.calls) != n {
		t.Fatalf("second List within TTL ran systemctl again")
	}
	for _, argv := range f.calls {
		if argv[1] != "--user" {
			t.Fatalf("systemctl without --user: %v", argv)
		}
	}
}

func TestUnitsAct(t *testing.T) {
	u, f := newFakeUnits()
	svc, err := u.Act(context.Background(), "relay.service", "restart")
	if err != nil || svc.Name != "relay.service" {
		t.Fatalf("Act = %+v %v", svc, err)
	}
	var sawRestart bool
	for _, argv := range f.calls {
		if argv[3] == "restart" {
			sawRestart = true
			if argv[len(argv)-1] != "relay.service" {
				t.Fatalf("argv = %v", argv)
			}
		}
	}
	if !sawRestart {
		t.Fatal("restart not issued")
	}
	if _, err := u.Act(context.Background(), "nope.service", "start"); statusOf(err) != 404 {
		t.Fatalf("unknown unit: %v", err)
	}
	if _, err := u.Act(context.Background(), "relay.service", "enable"); statusOf(err) != 400 {
		t.Fatalf("bad action: %v", err)
	}
	f.fail["start"] = errors.New("boom")
	if _, err := u.Act(context.Background(), "relay.service", "start"); err == nil {
		t.Fatal("systemctl failure must surface")
	}
}

func TestUnitsNoSystemd(t *testing.T) {
	u := &Units{}
	if _, err := u.List(context.Background()); !errors.Is(err, ErrNoSystemd) {
		t.Fatalf("err = %v", err)
	}
}
