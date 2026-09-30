package api

import "time"

// Additive contract types owned by the notify/clip/snippets/schedule/search
// features. Mirrored in web/src/api/notify.ts.

// NotificationsRead is the payload of EvNotificationRead.
type NotificationsRead struct {
	IDs []string `json:"ids"`
	All bool     `json:"all,omitempty"`
}

// CronPreview answers GET /api/v1/schedules/describe?cron=&timezone= so the
// schedule editor can show "Every weekday at 02:00" and the next runs as
// the user types.
type CronPreview struct {
	Valid       bool        `json:"valid"`
	Description string      `json:"description,omitempty"`
	Next        []time.Time `json:"next,omitempty"`
	Error       string      `json:"error,omitempty"`
}
