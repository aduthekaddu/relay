# Notifications, clipboard and schedules (backend)

Owners: `internal/notify`, `internal/clip`, `internal/schedule`. Routes are
listed in `API.md`; this file explains behaviour.

## Notifications (`internal/notify`)

`notify.Service` implements `core.Notifier` and is set as `Deps.Notifier`
in `app/wire_notify.go`. Every feature (agents' attention hooks, previews,
schedules, security) calls `Notifier.Notify`.

### Pipeline

```
Notify(req)
  normalize        kind → known kind or "custom"; title ≤ 200, body ≤ 4000,
                   link ≤ 2048 and must be an in-app path ("/…");
                   defaults to /terminal/<sessionId> when a session is set
  dedupe           same sessionId + kind within 30 s → the earlier
                   notification is returned, nothing new is stored
  inbox            INSERT, prune to the newest 500, publish EvNotification
  shouldDeliver    rules → quiet hours → presence → any channel configured
  enqueue          bounded queue (256); full queue drops + logs, never blocks
worker             fans the job out to push / ntfy / webhook concurrently
```

`Notify` does one SQLite insert and a channel send; it never waits for the
network. Marking read publishes `EvNotificationRead` with
`api.NotificationsRead{ids, all}`.

### Rules

| Kind                           | Default | Bypasses quiet hours |
| --------------------------------| ---------| ----------------------|
| attention                      | on      | yes                  |
| security                       | on      | yes                  |
| done, system, schedule, custom | on      | no                   |
| exited, preview                | off     | no                   |

A rule that is off only stops external delivery; the inbox always stores
the notification. Quiet hours are `HH:MM` local times and may wrap midnight
(`22:00`–`07:00`); equal start and end disables them.

Presence suppression: when the notification carries a `sessionId` and
`Deps.Presence.Watching(sessionId)` is true (someone has that terminal open
and visible), push, ntfy and webhook are skipped. `wireLive` installs the
canonical `core.Presence` before `wireNotify`; notify reads `Deps.Presence`
when deciding whether to enqueue delivery. A nil presence means no watcher.
Finalize service fields during wiring before concurrent work starts. The inbox
and its public event are retained even when external delivery is suppressed.

### Settings

`GET/PATCH /api/v1/notify/settings` (`api.NotifySettings`). Defaults come
from `relay.toml [notify]`; once saved from the UI they live in the KV key
`notify.settings`. `devices` and `vapidKey` are read-only. ntfy and webhook
URLs must be `http(s)`; they are never logged (errors show only the host).

### Web Push

- VAPID keys are generated on first start and stored in KV
  `notify.vapid.public` / `notify.vapid.private`. The private key never
  leaves the process.
- `POST /api/v1/push/subscribe` stores the subscription with a device label
  derived from the User-Agent ("iPhone · Safari"); at most 50 devices.
- Sent with `webpush-go`: TTL 12 h, urgency `high` for attention/security,
  `normal` otherwise. Payload (encrypted, aes128gcm):

  ```json
  {"title":"Claude needs you","body":"Approve edit?","link":"/terminal/abc","tag":"attention:abc","kind":"attention"}
  ```

- A push service answering 404 or 410 means the subscription is gone; it is
  deleted. `POST /api/v1/push/test` sends to every device and reports how
  many accepted.

### ntfy and webhook

- ntfy: `POST <topic URL>` with the body as text and headers `Title`,
  `Priority` (security 5, attention 4, exited/preview 2, else 3), `Tags`
  (kind emoji, `relay`, kind) and `Click` (absolute link when the public URL is
  known). Credentials may be embedded in the URL. Timeout 10 s.
- Webhook: `POST` JSON = the `Notification` plus `url`, `text`, `content`
  (Slack/Discord compatible) and `source: "relay"`. Timeout 5 s.

### Sending from scripts

`POST /api/v1/notify` accepts only the local control socket or an API token
(browser sessions cannot inject notifications).

```sh
relay notify "build finished"                       # kind custom
relay notify -t "Deploy" --kind done --link /terminal/$RELAY_SESSION ok
make test 2>&1 | tail -3 | relay notify -t "Tests"  # message from stdin
```

`RELAY_SESSION` (set in every Relay terminal) is attached as `sessionId`,
which enables dedupe and presence suppression.

## Universal clipboard (`internal/clip`)

- Table keeps the newest 200 clips, each ≤ 256 KiB of valid UTF-8; an entry
  identical to the newest one refreshes that entry instead of adding a
  duplicate.
- Sources: `POST /api/v1/clip` (browser copy, `relay clip`), and
  `core.BusClipCapture` events (OSC 52 from terminals, desktop clipboard).
  Every stored clip publishes `EvClip`.
- Terminal wiring publishes `core.ClipCapture`; successful desktop clipboard
  reads and writes publish the same payload with source `desktop`. Empty or
  failed desktop operations do not publish captures. Reading through the existing
  desktop API captures external X clipboard text; automatic viewer synchronization
  and real device verification remain separate work.
- `clip.New` subscribes synchronously during wiring, before `App.Run` starts
  producers. `Start` consumes that bounded subscription and releases it on exit;
  app cleanup also closes it if wiring fails before startup. The bus is best
  effort: its 64-event capture queue can drop events when full. Nil bus disables
  capture while HTTP clipboard operations still work.
- The handler accepts canonical values/pointers and retains legacy strings,
  byte slices and JSON-compatible Text/Source structs/maps. Invalid and oversized
  captures are rejected without stopping the loop. UTF-8 repair must also fit
  within 256 KiB. The desktop API keeps its separate existing 1 MiB limit, so
  desktop success does not guarantee history acceptance.
- `core.BusClipCapture` is backend-only. Browser subscriptions cannot receive
  raw captures; live emits only the validated public `EvClip` history event.

```sh
git rev-parse HEAD | relay clip   # copy
relay clip -p                     # print the latest clip
relay clip --list -n 5            # recent clips
```

## Schedules (`internal/schedule`)

- Cron: standard 5 fields or descriptors (`@daily`, `@hourly`, …) via
  robfig/cron, evaluated in the schedule's IANA timezone (default: server
  local). `@every` and seconds fields are rejected.
- `GET /api/v1/schedules/describe?cron=&timezone=&count=3` →
  `api.CronPreview` (`"Every weekday at 02:00"`, next runs) for the editor.
- A run is a `task` terminal created through ptyd in the schedule's cwd:
  either `Agents.HeadlessCommand(agent, prompt)` or the literal argv
  command. The runner waits for exit (ptyd events, polling as fallback)
  with a 2 h maximum, then records a `ScheduleRun` with status, exit code
  and the last 40 output lines (`Pty.Snapshot`), publishes `EvScheduleRun`
  and, when `notify` is set, sends a `schedule` notification.
- A schedule never overlaps itself: a due run while one is active is
  recorded as `skipped`. Runs left `running` by a restart are resumed or
  closed on start. The last 50 runs per schedule are kept.
- `[schedules] enabled = false` in relay.toml stops the timer; "Run now"
  still works.
