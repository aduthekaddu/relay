// Additive contract types owned by the notify/clip/snippets/schedule/search
// features. Mirrors internal/api/notify.go.

/** Payload of the `notification.read` event. */
export interface NotificationsRead {
  ids: string[]
  all?: boolean
}

/** GET /api/v1/schedules/describe?cron=&timezone=&count= */
export interface CronPreview {
  valid: boolean
  description?: string
  next?: string[]
  error?: string
}
