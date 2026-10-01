# Relay HTTP API (v1)

Base path `/api/v1`. JSON bodies, camelCase fields. Types are defined in
`internal/api/types.go` and mirrored in `web/src/api/types.ts`. Errors use
`{"error":{"code","message","field?","retryIn?"}}` with the matching HTTP
status. Timestamps are RFC 3339.

Auth: browser session cookie `relay_session` (`__Host-relay_session` over
HTTPS), `Authorization: Bearer rly_<token>`, or the local control socket.
Unsafe cookie requests and every cookie WebSocket must send an allowed
`Origin` (the canonical origin from config).

Legend: 🔓 public · 🔌 WebSocket · ⏳ long-running (202 + events).

## info (owner: info)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET 🔓 | `/api/v1/health` | → `{ok:true, version}` (no secrets) |
| GET | `/api/v1/info` | → `Info` |
| GET | `/api/v1/settings` | → `SettingsState` (saved and effective values) |
| PATCH | `/api/v1/settings` | `Partial<Settings>` → `SettingsState` (atomic save; consumer timing reported) |

Settings metadata is read-only. Validation errors return 400; unknown TOML
fields return 409; configuration read/save failures return 500 without
publishing an effective update. See [runtime settings](RUNTIME_SETTINGS.md)
for all six fields, daemon compatibility and existing-session behavior.
Types are in `internal/api/runtime_settings.go` and
`web/src/api/runtime-settings.ts`.

Info includes read-only [feature capabilities](CAPABILITIES.md): configured,
available, effective and lifecycle state from preview/apps owners. The
managed Apps and Desktop responses expose the same additive snapshots.
Queries do not launch optional services.

## auth (owner: auth)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET 🔓 | `/api/v1/auth/state` | → `AuthState` |
| POST 🔓 | `/api/v1/auth/setup` | `{username,password}` → `LoginResponse` (only when `setupRequired`; sets cookie) |
| POST 🔓 | `/api/v1/auth/login` | `LoginRequest` → `LoginResponse` (+ Set-Cookie). 401 bad credentials, 429 rate-limited |
| POST | `/api/v1/auth/logout` | → 204 (clears cookie, revokes session) |
| POST 🔓 | `/api/v1/auth/passkey/begin` | → WebAuthn `PublicKeyCredentialRequestOptions` JSON (discoverable) |
| POST 🔓 | `/api/v1/auth/passkey/finish` | credential JSON → `LoginResponse` (+ Set-Cookie) |
| GET | `/api/v1/auth/passkeys` | → `Passkey[]` |
| POST | `/api/v1/auth/passkeys/begin` | `{name}` → WebAuthn creation options |
| POST | `/api/v1/auth/passkeys/finish` | credential JSON → `Passkey` |
| PATCH | `/api/v1/auth/passkeys/{id}` | `{name}` → `Passkey` |
| DELETE | `/api/v1/auth/passkeys/{id}` | → 204 |
| POST | `/api/v1/auth/password` | `ChangePasswordRequest` → 204 (revokes other sessions) |
| GET | `/api/v1/auth/totp` | → `{enabled}` |
| POST | `/api/v1/auth/totp/setup` | → `TOTPSetup` |
| POST | `/api/v1/auth/totp/enable` | `{code}` → 204 |
| POST | `/api/v1/auth/totp/disable` | `{code}` → 204 |
| GET | `/api/v1/auth/sessions` | → `DeviceSession[]` |
| DELETE | `/api/v1/auth/sessions/{id}` | → 204 |
| POST | `/api/v1/auth/sessions/revoke-others` | → `{revoked:n}` |
| GET | `/api/v1/auth/tokens` | → `APIToken[]` |
| POST | `/api/v1/auth/tokens` | `{name}` → `CreatedToken` |
| DELETE | `/api/v1/auth/tokens/{id}` | → 204 |
| GET | `/api/v1/auth/activity?limit=` | → `AuditEntry[]` |

Security requirements: argon2id hashes; constant-time compares; per-IP and
global login rate limits with exponential backoff; generic error messages;
new-device sign-in → `security` notification; session ids are 256-bit
random, stored hashed; sliding expiry; cookies `HttpOnly; SameSite=Lax;
Secure` (unless `insecure_cookies`).

## live events (owner: live)

| Method | Path | |
| --- | --- | --- |
| GET 🔌 | `/api/v1/events` | server → `Event` JSON text frames; client → `ClientEvent` |

On connect the server sends `hello` with `Info`, then a snapshot:
`terminal.updated` for each live terminal. Clients subscribe to `metrics`
explicitly (1 Hz while subscribed). `visibility` messages tell the server
which route the user is looking at, so the notifier can skip pushing a
notification for the terminal already on screen. Ping every 25 s.

Feature owners publish `capabilities.changed` with `{feature}` to invalidate
cached Info. See [capability events and timing](CAPABILITIES.md).

## terminals & uploads (owner: terminal)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/terminals` | → `TerminalSession[]` (includes importable tmux sessions as `kind:"tmux"` with `meta.importable="1"`) |
| POST | `/api/v1/terminals` | `CreateTerminalRequest` → `TerminalSession` |
| GET | `/api/v1/terminals/{id}` | → `TerminalSession` |
| PATCH | `/api/v1/terminals/{id}` | `UpdateTerminalRequest` → `TerminalSession` |
| DELETE | `/api/v1/terminals/{id}?signal=TERM` | → 204 (kill; `?forget=1` also removes the exited record) |
| POST | `/api/v1/terminals/{id}/input` | `TerminalInput` → 204 |
| POST | `/api/v1/terminals/{id}/resize` | `{cols,rows}` → 204 |
| GET | `/api/v1/terminals/{id}/snapshot?lines=40` | → `TerminalSnapshot` |
| POST | `/api/v1/terminals/{id}/attention/ack` | → 204 (clear "needs you") |
| GET 🔌 | `/api/v1/terminals/{id}/attach?cols=&rows=&replay=1` | terminal protocol (below) |
| GET | `/api/v1/terminals/{id}/recording` | → asciicast v2 (`application/x-asciicast`) |
| POST | `/api/v1/uploads` | `StartUploadRequest` → `Upload` |
| PUT | `/api/v1/uploads/{id}?offset=N` | raw bytes → `Upload` (resume from `received`) |
| POST | `/api/v1/uploads/{id}/complete` | → `UploadResult` |
| DELETE | `/api/v1/uploads/{id}` | → 204 |

### Terminal WebSocket protocol

- server → client **binary**: raw PTY output (UTF-8 byte stream; may split
  multibyte sequences — use `term.write(Uint8Array)`).
- server → client **text**: `TermServerMsg` — `hello` (session, cols, rows,
  readOnly), `replay-begin`/`replay-end` around scrollback replay, `title`,
  `cwd`, `resize` (authoritative PTY size), `bell`, `notify` (OSC 9/777),
  `attention`, `clients`, `exit` (code), `error`, `pong`.
- client → server **binary**: input bytes.
- client → server **text**: `TermClientMsg` — `resize` {cols, rows},
  `focus` {visible}, `ping`, `ack` {bytes} (flow control: the server pauses
  a client that is more than 1 MiB behind its acknowledgements).
- Size policy: the most recently *active* client (typed or resized last)
  sets the PTY size ("latest wins", like tmux `window-size latest`).

## agents (owner: agents)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/agents` | → `AgentInfo[]` |
| GET | `/api/v1/agents/sessions?agent=&q=&cwd=&status=live\|history\|all&pinned=&archived=&limit=&cursor=` | → `Page<AgentSession>` (sorted: live first, then updatedAt desc) |
| GET | `/api/v1/agents/sessions/{id}` | → `AgentSession` |
| PATCH | `/api/v1/agents/sessions/{id}` | `UpdateAgentSessionRequest` → `AgentSession` |
| GET | `/api/v1/agents/sessions/{id}/transcript?limit=200&before=<msgId>` | → `Transcript` (newest page first; messages oldest→newest within page) |
| POST | `/api/v1/agents/sessions/{id}/resume` | `ResumeAgentRequest` → `TerminalSession` |
| POST | `/api/v1/agents/launch` | `LaunchAgentRequest` → `TerminalSession` |
| GET | `/api/v1/agents/search?q=&agent=&limit=` | → `SearchHit[]` (FTS5 over user + assistant text) |
| GET | `/api/v1/agents/usage?range=today\|7d\|30d\|all` | → `UsageSummary` |
| GET | `/api/v1/agents/quotas` | → `Quota[]` |
| POST | `/api/v1/agents/{agent}/hooks` | → `HookStatus` (install attention hooks; backs up the agent config first) |
| DELETE | `/api/v1/agents/{agent}/hooks` | → `HookStatus` |
| POST | `/api/v1/agents/reindex` | → 202 ⏳ |

Session ids are `<agent>:<nativeId>`; always `encodeURIComponent` them.

## workspaces & git (owner: workspaces)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/workspaces` | → `Workspace[]` |
| POST | `/api/v1/workspaces/pin` | `{path, pinned}` → `Workspace` |
| GET | `/api/v1/workspaces/git/status?path=` | → `GitStatus` |
| GET | `/api/v1/workspaces/git/diff?path=&file=&staged=` | → `GitDiff` (≤ 1 MiB, `truncated`) |
| GET | `/api/v1/workspaces/git/log?path=&limit=30` | → `Commit[]` |
| POST | `/api/v1/workspaces/git/stage` | `GitActionRequest{path, files}` → `GitStatus` |
| POST | `/api/v1/workspaces/git/unstage` | same → `GitStatus` |
| POST | `/api/v1/workspaces/git/discard` | same → `GitStatus` (destructive: UI confirms) |
| POST | `/api/v1/workspaces/git/commit` | `{path, message}` → `GitStatus` |
| POST | `/api/v1/workspaces/git/push` | `{path}` → 202 ⏳ runs in a task terminal |
| POST | `/api/v1/workspaces/git/pull` | `{path}` → 202 ⏳ |
| POST | `/api/v1/workspaces/git/worktrees` | `{path, branch, base?}` → `Worktree` |
| DELETE | `/api/v1/workspaces/git/worktrees?path=` | → 204 |

## files (owner: files)

All `path` parameters are absolute or `~`-relative and must resolve inside
`files.root` (default: home). Symlinks resolving outside the root are
refused.

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/files/list?path=&hidden=&sort=name\|size\|mtime&desc=&offset=&limit=500` | → `DirListing` |
| GET | `/api/v1/files/stat?path=` | → `FileEntry` |
| GET | `/api/v1/files/raw?path=&download=1` | → bytes (Range supported; `Content-Security-Policy: sandbox`; attachment when `download` or not previewable) |
| GET | `/api/v1/files/text?path=` | → `{text, size, modTime, truncated, encoding}` (≤ 5 MiB) |
| PUT | `/api/v1/files/text?path=&mtime=` | `{text}` → `FileEntry` (409 if modified since `mtime`) |
| GET | `/api/v1/files/thumb?path=&size=256` | → JPEG/PNG thumbnail (cached) |
| POST | `/api/v1/files/mkdir` | `{path}` → `FileEntry` |
| POST | `/api/v1/files/touch` | `{path}` → `FileEntry` |
| POST | `/api/v1/files/rename` | `{path, name}` → `FileEntry` |
| POST | `/api/v1/files/move` | `{from[], to}` → `FileEntry[]` |
| POST | `/api/v1/files/copy` | `{from[], to}` → 202 ⏳ |
| POST | `/api/v1/files/delete` | `{paths[], trash?:true}` → 204 (trash = XDG Trash, restorable) |
| GET | `/api/v1/files/trash` | → `FileEntry[]` ; POST `/api/v1/files/trash/restore` `{paths}` ; POST `/api/v1/files/trash/empty` |
| GET | `/api/v1/files/zip?paths=a&paths=b` | → streamed zip |
| GET | `/api/v1/files/usage?path=` | → `DiskUsage` (`pending` while scanning) |
| GET | `/api/v1/files/search?q=&path=&content=0\|1&limit=` | → NDJSON stream of `FileSearchHit` |

Uploads into a folder use the generic upload API with `dir`.

## system (owner: system)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/system/metrics` | → `Metrics` (+ `metrics` events when subscribed) |
| GET | `/api/v1/system/metrics/history?minutes=60` | → `Metrics[]` (1 sample / 10 s ring buffer) |
| GET | `/api/v1/system/processes?sort=cpu\|mem&limit=100&q=` | → `Process[]` |
| POST | `/api/v1/system/processes/{pid}/signal` | `SignalRequest` → 204 (refuses protected) |
| GET | `/api/v1/system/services` | → `Service[]` (relay units + `systemd --user` units) |
| POST | `/api/v1/system/services/{name}/{start\|stop\|restart}` | → `Service` |
| GET 🔌 | `/api/v1/system/logs?unit=&file=&lines=200` | streams `LogLine` JSON (journalctl --user -f, or tail -F a file under home) |

## previews (owner: previews)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/previews` | → `Preview[]` |
| PATCH | `/api/v1/previews/{port}` | `UpdatePreviewRequest` → `Preview` |
| ANY | `/p/{port}/…` | path-mode proxy (sandboxed CSP, cookie stripped) |
| ANY | `https://{port}.{previews.host}/…` | subdomain-mode proxy (host-only cookie via `/_relay/preview-auth` handshake) |

## apps & desktop (owner: apps)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/apps` | → `App[]` |
| POST | `/api/v1/apps/{id}/start` · `/stop` | → `App` |
| ANY | `/apps/{id}/…` | reverse proxy to the running app (HTTP + WS), started on demand |
| GET | `/api/v1/desktop` | → `DesktopState` |
| POST | `/api/v1/desktop/start` · `/stop` | → `DesktopState` |
| POST | `/api/v1/desktop/launch` | `{app}` → `DesktopState` |
| POST | `/api/v1/desktop/clipboard` | `{text}` → 204 (set X clipboard) ; GET → `{text}` |
| POST | `/api/v1/desktop/resize` | `{width, height}` → `DesktopState` |
| GET 🔌 | `/api/v1/desktop/ws` | binary RFB stream bridged to Xvnc's unix socket (noVNC client) |

## notifications (owner: notify)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/notifications?limit=50&unread=` | → `Notification[]` |
| POST | `/api/v1/notifications/read` | `{ids?:[], all?:true}` → 204 |
| DELETE | `/api/v1/notifications/{id}` | → 204 |
| POST | `/api/v1/notify` | `NotifyRequest` → `Notification` (used by CLI/hooks; local or token) |
| GET | `/api/v1/notify/settings` | → `NotifySettings` |
| PATCH | `/api/v1/notify/settings` | `Partial<NotifySettings>` → `NotifySettings` |
| GET | `/api/v1/push/key` | → `{publicKey}` |
| POST | `/api/v1/push/subscribe` | `PushSubscription` → 204 |
| POST | `/api/v1/push/unsubscribe` | `{endpoint}` → 204 |
| POST | `/api/v1/push/test` | → 204 |

## clipboard, snippets, notes (owners: clip, snippets)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/clip?limit=50` | → `Clip[]` |
| POST | `/api/v1/clip` | `{text, source}` → `Clip` |
| DELETE | `/api/v1/clip/{id}` · `/api/v1/clip` | → 204 |
| GET/POST | `/api/v1/snippets` | → `Snippet[]` / `Snippet` |
| PATCH/DELETE | `/api/v1/snippets/{id}` | `Partial<Snippet>` → `Snippet` / 204 |
| POST | `/api/v1/snippets/{id}/use` | → `Snippet` (increments uses) |
| GET/POST | `/api/v1/notes` | → `Note[]` / `Note` |
| GET/PATCH/DELETE | `/api/v1/notes/{id}` | → `Note` |

## schedules (owner: schedule)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET/POST | `/api/v1/schedules` | → `Schedule[]` / `Schedule` |
| PATCH/DELETE | `/api/v1/schedules/{id}` | → `Schedule` / 204 |
| POST | `/api/v1/schedules/{id}/run` | → `ScheduleRun` |
| GET | `/api/v1/schedules/{id}/runs?limit=20` | → `ScheduleRun[]` |

## command center (owner: search)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/search?q=&scopes=a,b&limit=8` | → `SearchResponse` (federated over registered providers, ≤ 150 ms budget) |
| GET | `/api/v1/scripts` | → `ScriptCommand[]` (from `~/.config/relay/commands/*`) |
| POST | `/api/v1/scripts/{id}/run` | `RunScriptRequest` → `RunScriptResult` |
| POST | `/api/v1/ask` | `AskRequest` → NDJSON stream of `AskChunk` (headless agent run) |

## toolbox (owner: toolbox)

| Method | Path | Body → Response |
| --- | --- | --- |
| GET | `/api/v1/toolbox` | → `Tool[]` |
| POST | `/api/v1/toolbox/{id}/install` | → `TerminalSession` (the install runs visibly in a toolbox terminal) |
| GET | `/api/v1/toolbox/mcp` | → `MCPServer[]` |
| POST | `/api/v1/toolbox/mcp/apply` | `MCPApplyRequest` → `MCPServer` |

## CLI ↔ server (local control socket)

`$XDG_RUNTIME_DIR/relay/relay.sock` serves the same routes with principal
`local`. Inside a Relay terminal `RELAY_SESSION` names the session and
`RELAY_SOCKET` points at the socket:

| Command | Calls |
| --- | --- |
| `relay notify [-t title] [--kind done] msg` | POST /api/v1/notify |
| `relay clip` (stdin) / `relay clip -p` | POST/GET /api/v1/clip |
| `relay open <path[:line]>` | publishes `open` event → focused device opens the file |
| `relay preview <port>` | prints preview URL + QR |
| `relay ls` / `relay attach <id>` / `relay run -- cmd` | terminals API / ptyd attach |
| `relay hook <agent> <event>` | reads hook JSON on stdin → attention/done |
