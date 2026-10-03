package core

import "testing"

func TestBackendTopics(t *testing.T) {
	for _, tt := range []struct {
		topic   string
		backend bool
	}{
		{BusClipCapture, true}, {BusAudit, true}, {BusSessionRevoked, true},
		{"clip", false}, {"notification", false}, {"metrics", false}, {"clip.capture.extra", false}, {"", false},
	} {
		t.Run(tt.topic, func(t *testing.T) {
			if got := IsBackendTopic(tt.topic); got != tt.backend {
				t.Fatalf("backend=%v want %v", got, tt.backend)
			}
		})
	}
}
