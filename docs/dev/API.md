# Relay HTTP API (v1)

This inventory follows registrations and handlers in `internal/*`, including
raw proxies and the private ptyd API. Base path `/api/v1`; JSON uses camelCase.
The canonical structs are [internal/api](../../internal/api), mirrored in
[web/src/api](../../web/src/api). Feature supplements include auth, agents,
files, toolbox, notify, previews, capabilities and runtime settings.

## Shared transport and error rules

Tables list each registered method separately. **A** means authenticated by
browser cookie (`relay_session`, `__Host-relay_session` on HTTPS), API bearer
token, or local control socket; **P** means public; **C** means cookie/local
account administration (API tokens receive 403); **T** means token/local only.
🔌 denotes a WebSocket upgrade (101). GET registrations also accept HEAD via
Go ServeMux; HEAD does not establish a socket. Unregistered paths/methods
return 404/405, potentially plain text. Trailing-slash routing may redirect.

Every A/C/T row additionally has 401 for no valid principal. Unsafe cookie
requests and cookie WebSocket upgrades require an allowed Origin (403).
Public auth POST handlers also enforce Origin, with their local exemption.
Raw handlers apply their own rules as described below. See [SECURITY.md](SECURITY.md)
and [AUTH.md](AUTH.md). Cookie/token API WebSockets revalidate credentials,
reject input after revocation and close within 3 seconds using 1008 and the
fixed reason `authentication ended`; unresponsive peers may close abruptly.
Local principals are exempt. This guarantee covers events, terminal attach,
desktop and logs; it does not describe upstream proxy sockets.

JSON errors use `ErrorBody`; a synthetic, secret-free example is:

```json
{"error":{"code":"not_found","message":"item not found"}}
```

`code` and `message` are required; `field` and `retryIn` (seconds) are optional.
`httpx.Fail` sets Retry-After when retryIn is positive, including some 502
responses. Error columns list handler-specific failures; **all JSON handlers
can also return 500/internal for unclassified storage, I/O or service errors**.
Decoder errors are 400/bad_request. Ordinary `httpx.Decode` limits input to
1 MiB and rejects unknown fields; feature limits are noted below. It reads
one JSON value and does not promise rejection of trailing values. Optional
request fields can be omitted; zero/default interpretation is feature specific.
Request bodies shown as “—” are unused, rather than mandatory empty JSON.

**F** in errors means the shared files resolver/I/O family: 400/bad_request
(invalid path/input or unsuitable target), 403/forbidden (outside root,
permissions, protected root or special file), 404/not_found (missing),
409/conflict (collision, changed target or incompatible operation), and 500
for other failures. Stream and proxy errors after headers cannot change the
HTTP status. WebSocket handshake errors can be plain text.

Timestamps are RFC 3339 strings. `omitempty` makes scalar/slice/map/pointer
fields optional; a value `time.Time` still emits `0001-01-01T00:00:00Z` when
zero, even with `omitempty` (for example FileJob.endedAt while running).
Required nil slices/maps and required pointers can encode null. Browser
mirrors describe canonical producers, not runtime validation; generic
decoding retains unknown fields/topics and missing data. IDs are opaque;
encodeURIComponent path IDs (agent sessions in particular).

## Info and settings (internal/info)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/health` | P | — → 200 `{ok,version}` | — |
| GET | `/api/v1/info` | A | — → 200 `Info` | — |
| GET | `/api/v1/settings` | A | — → 200 `SettingsState` | 409 unknown saved TOML fields; 500 read |
| PATCH | `/api/v1/settings` | A | partial `Settings` → 200 `SettingsState` | 400 validation; 409 unknown TOML fields; 500 read/save |

Only workspaceRoots, defaultShell, defaultCwd, recordAgents, claudeQuota and
idleMinutes are editable. recordingMode, effective and apply are read-only.
Saved values stay flat; effective is consumer state and apply reports timing
(next-request, next-session, next-idle-check, restart-required, different-config,
unavailable). effective.terminal can be null; terminalStatus distinguishes
next-session, restart-required, different-config and unavailable. Existing
sessions retain their defaults. Persistence failure publishes no effective
update. See [RUNTIME_SETTINGS.md](RUNTIME_SETTINGS.md) for atomic merging and
the daemon compatibility boundary.

Info.capabilities is a read-only snapshot from preview, Code and Desktop
owners. Configured mode/enabled, available prerequisites, effective preview
mode and lifecycle readiness are separate. Features contains compatibility
flags; installation or explicit subdomain configuration proves neither
process readiness nor DNS/TLS. GETs do not launch optional services. Apps and
Desktop expose the same additive snapshots; capabilities.changed invalidates
cached Info. See [CAPABILITIES.md](CAPABILITIES.md).

## Auth (internal/auth)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/auth/state` | P | — → 200 `AuthStateResponse` | — |
| POST | `/api/v1/auth/setup` | P | `SetupRequest` → 200 `LoginResponse`, session cookie | 400 username/password; 403 origin/setup code; 409 account exists; 429 throttle |
| POST | `/api/v1/auth/login` | P | `LoginRequest` → 200 `LoginResponse`, cookie on success | 400 invalid input; 401 credentials/TOTP; 403 origin; 409 no account; 429 throttle |
| POST | `/api/v1/auth/logout` | A | — → 204, clears cookie/revokes cookie session | — |
| POST | `/api/v1/auth/passkey/begin` | P | — → 200 WebAuthn assertion options | 403 origin; 409 no account; 429 throttle; 503 unavailable |
| POST | `/api/v1/auth/passkey/finish` | P | credential JSON → 200 `LoginResponse`, cookie | 400 expired request; 401 verification; 403 origin; 429 throttle |
| GET | `/api/v1/auth/passkeys` | A | — → 200 `PasskeyDetail[]` | — |
| POST | `/api/v1/auth/passkeys/begin` | C | optional `NameRequest` → 200 WebAuthn creation options | 400 name; 403 token; 409 no account; 503 unavailable |
| POST | `/api/v1/auth/passkeys/finish` | C | credential JSON → 201 `PasskeyDetail` | 400 expired/invalid; 403 token; 409 duplicate/no account |
| PATCH | `/api/v1/auth/passkeys/{id}` | C | `NameRequest` → 200 `PasskeyDetail` | 400 name; 403 token; 404 missing |
| DELETE | `/api/v1/auth/passkeys/{id}` | C | — → 204 | 403 token; 404 missing |
| POST | `/api/v1/auth/password` | C | `ChangePasswordRequest` → 204, revokes other sessions | 400 current/next; 403 token; 409 no account |
| GET | `/api/v1/auth/totp` | A | — → 200 `TOTPStatus` | — |
| POST | `/api/v1/auth/totp/setup` | C | — → 200 `TOTPSetup` | 403 token; 409 no account/already enabled |
| POST | `/api/v1/auth/totp/enable` | C | `CodeRequest` → 204 | 400 code; 403 token; 409 no account/already enabled/not set up |
| POST | `/api/v1/auth/totp/disable` | C | `CodeRequest` → 204 | 400 code; 403 token; 409 no account/already off |
| GET | `/api/v1/auth/sessions` | A | — → 200 `DeviceSession[]` | — |
| DELETE | `/api/v1/auth/sessions/{id}` | A | — → 204 | 404 missing |
| POST | `/api/v1/auth/sessions/revoke-others` | A | — → 200 `RevokedCount` | — |
| GET | `/api/v1/auth/tokens` | A | — → 200 `APIToken[]` | — |
| POST | `/api/v1/auth/tokens` | C | `NameRequest` → 201 `CreatedToken` | 400 name; 403 token |
| DELETE | `/api/v1/auth/tokens/{id}` | A | — → 204 | 404 missing |
| GET | `/api/v1/auth/activity?limit=100` | A | limit clamped 1–500 → 200 `AuditEntry[]` | — |

AuthStateResponse extends AuthState with optional setupCodeRequired and
required passkeysAvailable. SetupRequest has username/password and optional
code/remember; remote first setup requires the one-time code. LoginRequest
has required remember, optional totp; missing TOTP can return 200 with
needTotp and no signed-in cookie. PasskeyDetail adds synced, optional transports
and aaguid. NameRequest.name and CodeRequest.code are required fields (passkey
begin can omit its body). CreatedToken.token is returned once; TOTPSetup
contains provisioning material. These are intentional secret-bearing response
contracts: do not use real responses as documentation examples or logs.
See AUTH.md for cookie expiry, sliding sessions, generic credential failures,
per-IP/global admission and exponential backoff. 429 uses retryIn/Retry-After.

## Live events (internal/live)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET 🔌 | `/api/v1/events` | A | upgrade → 101, text `Event`; client `ClientEvent` | handshake 400/403; auth 401 |

Event is `{type,at,data?}`. On connect: hello(Info), then terminal.updated
snapshots. No durable history/replay. Client messages: subscribe/unsubscribe
with topics?, visibility with visible?/path?, ping. Up to 16 valid topic names;
64 KiB read limit; unknown/invalid client messages are ignored. Application
ping yields pong with no data; control pings run every 25 seconds. Server send
queue holds 256 frames; slow consumers disconnect. Metrics require a metrics
subscription and visible tab; the sampler runs at 1 Hz only when needed.
Non-text input is ignored; compression is disabled. Writes time out after
10 seconds, control ping after 15 seconds. Initial-state failure closes with
1011, write/ping failure with 1001, normal closure with 1000; queue overflow
tears down the socket without a close handshake. Authentication closure is
the shared 1008 contract above.

Known browser payloads (EventMap in web/src/api/events.ts):

| Topic | Canonical data / source |
| --- | --- |
| `hello` | `Info` (live) |
| `terminal.created` | `TerminalSession` (terminal) |
| `terminal.updated` | `TerminalSession` (terminal) |
| `terminal.exited` | `TerminalSession` (terminal) |
| `terminal.removed` | `{id:string}` (terminal) |
| `agents.indexed` | `{agent:string,sessions:number}` (agent indexer) |
| `agents.session` | `AgentSession` (agents) |
| `notification` | `Notification` (notify) |
| `notification.read` | `NotificationsRead`: required ids[], optional all (notify) |
| `metrics` | `Metrics` (system) |
| `previews.changed` | `Preview[]` (can encode null for an empty nil slice) |
| `clip` | `Clip` (clip) |
| `open` | `OpenRequest`: path, optional line (files/terminal) |
| `schedule.run` | `ScheduleRun` (schedule) |
| `app.state` | `App` (apps) |
| `desktop.state` | `DesktopState` (apps) |
| `toolbox.job` | `ToolboxJob`: tool, terminalId, state, exitCode? (toolbox) |
| `files.job` | `FileJob` (files) |
| `workspace.changed` | `{path:string}` (workspaces) |
| `capabilities.changed` | `CapabilityChange`: feature (previews/code/desktop) |
| `pong` | no data (live application ping reply) |

Toolbox state is running/done/failed, never a status field. Exit code is absent
while running or if the terminal disappeared; zero is a real exit code.
FileJob contains id, op(copy), state(running/done/failed/canceled), from[], to,
files/totalFiles, bytes/totalBytes, skipped, startedAt; current/error/result[]/
endedAt have omitempty tags (see zero timestamp rule). Totals may be zero
before scanning; partial result/progress is not an atomic transaction.

The browser decoder ignores non-text frames, malformed JSON and envelopes
without a string type. It forwards unknown string topics and their data to
wildcard subscribers unchanged, including absent data. Known subscriptions
are typed from canonical contracts; there is no runtime payload allowlist or
schema validation. Backend topics below are filtered by the server, not added
to EventType. Client handlers throwing do not stop other handlers.

## Terminals and uploads (internal/terminal)

**D** errors below: 404/not_found, 503/unavailable for missing daemon,
504/timeout for deadline, forwarded daemon statuses, otherwise 500/internal.

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/terminals` | A | — → 200 `TerminalSession[]` | D |
| POST | `/api/v1/terminals` | A | `CreateTerminalRequest` → 201 `TerminalSession` | 400 kind/input; D |
| GET | `/api/v1/terminals/{id}` | A | — → 200 `TerminalSession` | D |
| PATCH | `/api/v1/terminals/{id}` | A | `UpdateTerminalRequest` → 200 `TerminalSession` | 400 input; D |
| DELETE | `/api/v1/terminals/{id}?signal=&forget=` | A | — → 204 confirmed kill/forget | 400 signal; 409 daemon confirmation incompatibility; D |
| POST | `/api/v1/terminals/{id}/input` | A | `TerminalInput` → 204 | 400 decode; D |
| POST | `/api/v1/terminals/{id}/resize` | A | `{cols,rows}` → 204 | 400 bounds; D |
| GET | `/api/v1/terminals/{id}/snapshot?lines=40` | A | lines clamped 1–5000 → 200 `TerminalSnapshot` | D |
| POST | `/api/v1/terminals/{id}/attention/ack` | A | — → 204 | D |
| POST | `/api/v1/terminals/{id}/restore` | A | — → 201 new `TerminalSession` | 409 still running; D |
| GET | `/api/v1/terminals/{id}/recording?download=` | A | — → 200 asciicast v2 bytes | D; 404 recording missing |
| GET 🔌 | `/api/v1/terminals/{id}/attach?cols=&rows=&replay=1&readonly=` | A | upgrade → 101 binary/text terminal protocol | D; handshake 400/403 |
| POST | `/api/v1/uploads` | A | `StartUploadRequest` → 201 `Upload` | F; 413 too_large; 429 rate_limited; 507 no_space |
| GET | `/api/v1/uploads/{id}` | A | — → 200 `Upload` resumable state | 404 unknown/consumed/stale; F |
| PUT | `/api/v1/uploads/{id}?offset=N` | A | raw bytes → 200 `Upload` | 400 bad_request for missing/invalid offset, interrupted for interrupted chunks; 409 offset_mismatch; 413 too_large; 404 missing; F; 507 no_space |
| POST | `/api/v1/uploads/{id}/complete` | A | — → 200 `UploadResult` | 409 incomplete/conflict on collision; 404 consumed/missing; F; 507 no_space |
| DELETE | `/api/v1/uploads/{id}` | A | — → 204 | 404 consumed/missing; F |

List may include importable tmux entries, kind tmux and meta.importable="1".
Create fields are optional: name, command[], cwd, env map, kind, agent, cols,
rows, record, meta. Patch accepts optional name/pinned. Resize requires cols
1–1000 and rows 1–500. Input requires data, paste? wraps bracketed paste.
Default delete sends HUP gracefully before forced termination; explicit
TERM/KILL/INT/HUP/QUIT/USR1/USR2 accepts SIG prefix and case normalization.
forget removes the record, killing a running session first and ignoring signal.
Restore requires an exited/lost session, retains command/cwd/env and stored
metadata, adds meta.restoredFrom, and returns a new opaque ID. Old ID remains.
Recording has application/x-asciicast, no Range handling in this bridge;
download=true/1 adds an attachment filename based on the public ID. It streams
with a 10 minute context; interrupted transfers cannot become JSON errors.

### Terminal socket frames

Server binary frames are raw UTF-8 PTY bytes, possibly split mid-character.
Text frames are TermServerMsg with required t and optional session, cols, rows,
code, title, cwd, message, clients, readOnly. t is hello, replay-begin,
replay-end, exit, title, cwd, resize, notify, bell, attention, clients, error
or pong. Zero-valued numeric fields may be omitted. Client binary is input;
text TermClientMsg uses resize(cols,rows), focus(visible), ping, ack(bytes).
Unknown/malformed messages are ignored. Input read limit is 1 MiB; control
pings every 30 seconds have a 15-second timeout; bridge writes time out after
30 seconds. Compression uses no context takeover. readonly=true/1 drops input and resize but permits
focus/ack/ping. replay defaults on unless exactly 0. Invalid attach sizes
become zero/default sizes. Last active client sets PTY size. After a client
starts acknowledging cumulative binary bytes, more than 1 MiB unacknowledged
blocks writes; a separate 1 MiB queued live-output overflow closes with 1013
`lagging`. Session exit uses 1000 `exited`; unexpected daemon disconnect uses
1013 `terminal daemon disconnected`. Input-side or ping failures may tear down
the browser socket abruptly (see [PTYD.md](PTYD.md)).

### Upload lifecycle

Start has name and size fields; missing/empty name is sanitized, size defaults to zero and must be nonnegative; dir/mime optional. Chunk size is
4 MiB. Without dir, uploads use the dated storage directory; names are
sanitized and completed files do not overwrite existing names. PUT offset
must be 0–received; an earlier offset truncates and rewrites the partial file.
GET lookup exposes id/chunkSize/received/size, including persisted partial
uploads after a serve restart, with no private metadata. Interrupted chunks
preserve resumable bytes. Completion requires received=size and consumes the
ID; cancel also consumes it. Rate limits: 60 starts/minute (retry 10 seconds),
64 unfinished (retry 60 seconds), configured max size; stale partials expire
after 24 hours with hourly sweeping. Folder authorization is rechecked at
completion. See [terminal mutation audit](#terminal-mutation-audit).

## Agents (internal/agents)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/agents` | A | — → 200 `AgentInfo[]` | — |
| GET | `/api/v1/agents/sessions?agent=&q=&cwd=&status=&pinned=&archived=&limit=&cursor=` | A | → 200 `Page<AgentSession>` | 400 status/cursor |
| GET | `/api/v1/agents/sessions/{id}` | A | — → 200 `AgentSession` | 404 missing |
| PATCH | `/api/v1/agents/sessions/{id}` | A | `UpdateAgentSessionRequest` → 200 `AgentSession` | 400 title/decode; 404 missing |
| GET | `/api/v1/agents/sessions/{id}/transcript?limit=200&before=` | A | → 200 `Transcript` | 400 cursor; 404 missing |
| POST | `/api/v1/agents/sessions/{id}/resume` | A | optional `ResumeAgentRequest` → 200 `TerminalSession` | 400 input/cwd; 404 missing; 409 unsupported/agent not installed; 503 daemon unavailable |
| POST | `/api/v1/agents/launch` | A | `LaunchAgentRequest` → 200 `TerminalSession` | 400 agent/cwd/size; 404 agent missing; 409 agent not installed; 503 unavailable |
| GET | `/api/v1/agents/search?q=&agent=&limit=` | A | → 200 `SearchHit[]` | 429 rate_limited (retry 1 second) |
| GET | `/api/v1/agents/usage?range=` | A | → 200 `UsageSummary` | 400 invalid range |
| GET | `/api/v1/agents/quotas` | A | — → 200 `Quota[]` | — |
| POST | `/api/v1/agents/reindex?full=` | A | — → 202 `ReindexResponse` | — |
| POST | `/api/v1/agents/hook` | A | `AgentHookRequest` → 200 `AgentHookResult` | 400 hook/size; 404 agent missing |
| POST | `/api/v1/agents/{agent}/hooks` | A | — → 200 `HookStatus` | 404 agent; 409 unsupported/config failure; 503 executable missing |
| DELETE | `/api/v1/agents/{agent}/hooks` | A | — → 200 `HookStatus` | 404 agent; 409 unsupported/config failure |

Session IDs are agent:nativeId, URL-encoded. Session status is live/history/all;
limit defaults 50, clamped 1–200, live first then updatedAt descending. Transcript
limit defaults 200, clamped 1–1000; before is an opaque message ID; pages newest
first, messages oldest first inside a page. Empty agent search returns []; long queries are truncated. Search limit defaults 30, clamped
1–100; usage range today/7d/30d/all. Hook is intended for CLI via the local
socket, but registration is A, not a local-only restriction. Hook payload is
optional arbitrary agent JSON (1 MiB, with 4 KiB envelope allowance);
sessionId optional. Result action is attention/done/ignored, terminalId optional.
Reindex returns started, not an index completion promise. Missing provider
quota/usage data does not imply verified real provider integration.

## Workspaces and Git (internal/workspaces)

All path inputs are checked against workspace roots from the effective
runtime snapshot. **G** errors: 400 invalid path/files/branch/input, 403 outside
roots, 404 missing repo/path, 409 Git precondition/collision/in-use, 500 unclassified I/O
failure. Git command failures map to 409/conflict; missing Git is 503. Task
creation can additionally return 503 for unavailable daemon.

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/workspaces` | A | — → 200 `Workspace[]` | G |
| POST | `/api/v1/workspaces/pin` | A | `PinWorkspaceRequest` → 200 `Workspace` | G |
| GET | `/api/v1/workspaces/git/status?path=` | A | → 200 `GitStatus` | G |
| GET | `/api/v1/workspaces/git/diff?path=&file=&staged=` | A | → 200 `GitDiff` | G |
| GET | `/api/v1/workspaces/git/log?path=&limit=30` | A | → 200 `Commit[]` | G |
| POST | `/api/v1/workspaces/git/stage` | A | `GitActionRequest` path/files → 200 `GitStatus` | G |
| POST | `/api/v1/workspaces/git/unstage` | A | path/files → 200 `GitStatus` | G |
| POST | `/api/v1/workspaces/git/discard` | A | path/files → 200 `GitStatus` | G |
| POST | `/api/v1/workspaces/git/commit` | A | path/message → 200 `GitStatus` | G |
| POST | `/api/v1/workspaces/git/push` | A | path → 202 `GitTaskResponse` | G; 503 daemon |
| POST | `/api/v1/workspaces/git/pull` | A | path → 202 `GitTaskResponse` | G; 503 daemon |
| POST | `/api/v1/workspaces/git/worktrees` | A | path/branch/base? → 200 `Worktree` | G |
| DELETE | `/api/v1/workspaces/git/worktrees?path=` | A | → 204 | G; 503 cannot verify terminal usage |

All Git mutation bodies use GitActionRequest; unused optional fields do not
change the selected operation. GitTaskResponse.terminal is optional; 202 is
admission, not a successful push/pull. Diff is capped at 1 MiB with truncated?;
log limit defaults 30, clamped 1–200. Worktree removal refuses the main tree or
a tree in use by a terminal. Git commands use argv, not interpolated shell input.

## Files (internal/files)

Paths are absolute or ~ relative, resolved within files.root (default home).
Symlink escapes and special devices/sockets/pipes are refused as applicable.
Uploads use the generic API with dir. FileOpRequest fields are paths?, from?,
to?, path?, name?, trash?; only the operation's fields are meaningful.

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/files/list?path=&hidden=&sort=&desc=&offset=&limit=500` | A | → 200 `DirListing` | F; 400 sort |
| GET | `/api/v1/files/stat?path=` | A | → 200 `FileEntry` | F |
| GET | `/api/v1/files/raw?path=&download=1` | A | → bytes 200/206/304 | F; 416 invalid Range |
| GET | `/api/v1/files/text?path=` | A | → 200 `TextFile` | F |
| PUT | `/api/v1/files/text?path=&mtime=` | A | `SaveTextRequest` → 200 `FileEntry` | F; 413 bad_request |
| GET | `/api/v1/files/thumb?path=&size=256&v=` | A | → 200/206/304 image bytes | F; 413/415 bad_request; 416 Range; 503 unavailable on timeout |
| POST | `/api/v1/files/mkdir` | A | path → 200 `FileEntry` | F |
| POST | `/api/v1/files/touch` | A | path → 200 `FileEntry` | F |
| POST | `/api/v1/files/rename` | A | path/name → 200 `FileEntry` | F |
| POST | `/api/v1/files/move` | A | from[]/to → 200 `FileEntry[]` | F |
| POST | `/api/v1/files/copy` | A | from[]/to → 202 `FileJob` | F |
| POST | `/api/v1/files/delete` | A | paths[]/trash? → 204 | F |
| GET | `/api/v1/files/trash` | A | — → 200 `FileEntry[]` | F |
| POST | `/api/v1/files/trash/restore` | A | paths[] → 200 `FileEntry[]` | F |
| POST | `/api/v1/files/trash/empty` | A | — → 204 | F |
| GET | `/api/v1/files/zip?paths=a&paths=b` | A | → 200 streamed application/zip | F before headers |
| GET | `/api/v1/files/usage?path=&refresh=` | A | → 200 `DiskUsage` | F |
| GET | `/api/v1/files/search?q=&path=&content=&limit=` | A | → 200 NDJSON `FileSearchHit` | F; 400 query/nonfolder; 429 rate_limited (retry 1 second) |
| GET | `/api/v1/files/jobs` | A | — → 200 `FileJob[]` | — |
| DELETE | `/api/v1/files/jobs/{id}` | A | — → 204 cancellation request | 404 unknown job |
| POST | `/api/v1/open` | A | `OpenRequest` → 200 normalized `OpenRequest`, publishes open | F; 400 empty path |

List sort name/size/mtime/type, desc boolean; hidden defaults to configuration;
offset defaults 0, limit 500 clamped 1–5000. Directories sort before files.
Raw supports Range/conditional requests via ServeContent, nosniff and sandbox
CSP; attachment when download=1 or content is not previewable. TextFile includes
path/text/size/modTime/truncated/encoding. Text reads at most 5 MiB, encoding
utf-8/utf-8-bom/binary (binary text is empty); save enforces 5 MiB decoded text,
preserves BOM and returns 409 if supplied mtime no longer matches. New files
also return 200. Thumbnail sizes clamp 32–1024 and use cache buckets; sources
are limited to 64 MiB/40 million pixels; v selects immutable caching.

Copy queues work (four active slots); initial running job can have zero totals
before its bounded scan. It outlives the request, not a server restart. files.job
reports progress at most about 4 Hz and once at termination. Jobs are in memory,
sorted by startedAt descending, with finished jobs retained 10 minutes.
Cancel returns 204 for any retained job, including a finished one; running work
ends asynchronously as canceled, completed work stays completed. No rollback
of partial copies/moves/deletes. Trash defaults to files.use_trash, overridden
by optional trash; deleting entries already in trash is permanent. Trash target
is original path and modTime is deletion time; restore renames collisions.

Zip accepts repeated paths (up to 1000), preflights authorization, streams an
attachment; errors after output starts interrupt/log the transfer. Usage is
200 with pending/complete flags during asynchronous scans, never blanket 202.
Search q is trimmed, nonempty and at most 200 bytes; limit defaults 200, clamped
1–500; two concurrent searches. Name budget 3 seconds, content 10 seconds;
content uses ripgrep when present and a bounded Go fallback. NDJSON EOF is not
a completeness marker: late traversal/provider errors do not change 200.
Open normalizes to a resolved path; negative line or a directory removes line;
it broadcasts to connected browsers, without server-side focused-device routing.

## System (internal/system)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/system/metrics` | A | — → 200 `Metrics` | — |
| GET | `/api/v1/system/metrics/history?minutes=60` | A | → 200 `Metrics[]` | — |
| GET | `/api/v1/system/processes?sort=&limit=100&q=` | A | → 200 `Process[]` | 400 invalid sort |
| POST | `/api/v1/system/processes/{pid}/signal` | A | `SignalRequest` → 204 | 400 pid/signal; 403 protected/permission; 404 missing |
| GET | `/api/v1/system/services` | A | — → 200 `Service[]` | 503 systemd unavailable |
| POST | `/api/v1/system/services/{name}/{action}` | A | — → 200 `Service` | 400 name; 403 unmanaged; 404 missing/unknown action; 503 unavailable; 502 service_failed |
| GET 🔌 | `/api/v1/system/logs?unit=&file=&lines=200` | A | upgrade → 101 text `LogLine` | 400 source; 403 outside home; 404 file; 429 slots full (no retryIn); 503 source unavailable |

History is a 10-second sample ring; minutes clamped 1–60. Process sort is cpu
(default), mem, pid or name; limit clamped 1–2000. cpu sorts CPU descending,
then RSS descending then PID ascending; mem sorts RSS descending then PID;
pid ascending; name case-insensitive ascending then PID. q matches name/command, exact user/PID/terminal ID.
Signal accepts TERM/KILL/INT/HUP/STOP/CONT/QUIT/USR1/USR2, optional SIG prefix,
case-insensitive or numeric whitelist spelling; empty means TERM. Protected
processes cannot be signaled. Service action is start/stop/restart; manageAllUnits
controls unmanaged units. Unsupported systemd hosts return an empty service list.
Logs require exactly one of unit/file; file must resolve inside home, lines
clamped 0–2000 (invalid input defaults to 200). Text frames are LogLine (at,
prio, text, unit?); CloseRead handles control frames, and client application
frames can close the connection. Client read limit is 4 KiB; control pings
every 25 seconds and ping/write timeouts are 10 seconds. Reader slots are
bounded; source errors use 1011 `log source ended`, normal closure uses 1000.
No replay cursor.

## Previews (internal/previews)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/previews` | A | — → 200 `Preview[]` | — |
| GET | `/api/v1/previews/{port}/link` | A | — → 200 `PreviewLink` | 400 invalid/excluded port; 503 mode off |
| PATCH | `/api/v1/previews/{port}` | A | `UpdatePreviewRequest` → 200 `Preview` | 400 port/decode; 404 missing |

Link uses the owner's effective mode, not the saved configured mode; fields:
port/url/mode(subdomain or path; off during the race below)/listening, preview? when listening. If mode changes to off between admission and the snapshot, the current handler
can return 200 with mode off and an empty URL. A valid
non-listening port still returns 200; no startup/probe guarantee. Port must be
1–65535 and inside allowed ranges/not ignored. Patch accepts label?/pinned?/hidden?.

Raw registrations and host dispatch (these do not use the standard A wrapper):

| Surface | Method | Path | Contract |
| --- | --- | --- | --- |
| Raw redirect | ANY | `/p/{port}` | 308 to trailing slash, before authentication/port validation |
| Raw path proxy | ANY | `/p/{port}/` | manual authentication; anonymous HTTP GET/HEAD → 302 login, WS/others 401; cookie WS with refused Origin → 403; invalid/off/not listening → 404; HTTP and WS upstream forwarding, 502 proxy failure, sandbox CSP and cookie stripping |
| Raw auth helper | GET | `/_relay/preview-auth` | Relay-origin authentication; anonymous → 302 login; invalid port 400, wrong mode 404, issue failure 500; success 302 to subdomain callback |
| Host proxy | ANY | `https://{port}.{previews.host}/…` | only effective subdomain mode; excluded port 404; host preview cookie or API token; anonymous GET/HEAD → auth handshake 302, other methods 401; upstream HTTP/WS, 502 failure |
| Host callback | GET | `/_relay/preview-callback` | single-use expiring handshake; 403 invalid/expired/used; 500 issue failure; success 303 plus host-only HttpOnly/Secure/SameSite=Lax preview cookie; other methods 405 |

Raw path/host proxy responses are not ErrorBody and can carry upstream statuses.
The raw path proxy applies OriginAllowed to cookie-authenticated upgrade
attempts, including WebSockets and protocol lists: missing/unparseable/disallowed Origin or cross-site Fetch Metadata
returns 403 before forwarding. Other raw HTTP requests do not inherit Handle's
cookie Origin check; upstream socket semantics belong to the target. Opaque
`null` origins from sandboxed path previews are refused; browser HMR compatibility
has not been verified. See [PREVIEWS.md](PREVIEWS.md).

## Apps and desktop (internal/apps)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/apps` | A | — → 200 `App[]` | — |
| POST | `/api/v1/apps/{id}/start` | A | — → 200 `App` | 400 unmanaged; 404 missing; 503 prerequisites |
| POST | `/api/v1/apps/{id}/stop` | A | — → 200 `App` | 400 unmanaged; 404 missing |
| GET | `/api/v1/desktop` | A | — → 200 `DesktopState` | — |
| POST | `/api/v1/desktop/start` | A | — → 200 `DesktopState` | 503 prerequisites/start failed |
| POST | `/api/v1/desktop/stop` | A | — → 200 `DesktopState` | — |
| POST | `/api/v1/desktop/launch` | A | `{app}` → 200 `DesktopState` | 400 app; 404 unknown; 503 prerequisites/start failed |
| GET | `/api/v1/desktop/clipboard` | A | — → 200 `{text}` | 400 oversized text; 503 desktop unavailable |
| POST | `/api/v1/desktop/clipboard` | A | `{text}` → 204 | 400 decode/oversized text; 503 unavailable |
| POST | `/api/v1/desktop/resize` | A | `{width,height}` → 200 `DesktopState` | 400 bounds; 503 unavailable |
| GET 🔌 | `/api/v1/desktop/ws` | A | on-demand start, upgrade → 101 binary RFB | 409 viewer limit; 503 prerequisites/start/socket unavailable; handshake 400/403 |

App start admits asynchronously (state starting is not ready); lifecycle events
report outcomes. Desktop start/launch may wait up to 30 seconds. The desktop
app ID uses the desktop owner and returns App, not DesktopState. Clipboard
text is capped at 1 MiB; resize bounds are 320×240 through 8192×8192.
Desktop sockets start on demand before upgrade (up to 30 seconds), offer the
binary subprotocol with compression disabled, and forward binary RFB in both
directions. Client read limit is 8 MiB; server chunks are at most 64 KiB with
blocking backpressure. Nonbinary input closes with 1003 `binary frames only`;
normal peer closure uses 1000, desktop stop/connection loss/ping timeout uses
1001, authentication ending uses 1008. Control pings every 25 seconds have a
10-second timeout; writes have a 30-second timeout. Read-only capability
snapshots distinguish enabled, available and lifecycle; GET `/api/v1/desktop`
does not start the desktop.

| Surface | Method | Path | Contract |
| --- | --- | --- | --- |
| Raw redirect | ANY | `/apps/{id}` | 308 trailing slash before authentication |
| Raw app proxy | ANY | `/apps/{id}/` | missing 404; anonymous navigation 302 login, other 401; cookie unsafe/WS Origin check 403; unavailable/starting/failed 503 (Retry-After 1 while starting); manual authentication, on-demand start, upstream HTTP/WS and statuses, 502 failure |
| Raw web shell | ANY | `/` | public GET/HEAD assets/SPA/login/manifest/offline/service worker; 200/206/304/416 static responses; other methods 405; unmatched /api paths 404 |

## Notifications (internal/notify)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/notifications?limit=50&unread=` | A | → 200 `Notification[]` | — |
| POST | `/api/v1/notifications/read` | A | ids? / all? → 204 | 400 missing selection/too many IDs |
| DELETE | `/api/v1/notifications/{id}` | A | — → 204 | 404 missing |
| POST | `/api/v1/notify` | T | `NotifyRequest` → 200 `Notification` | 403 cookie principal; 400 invalid fields |
| GET | `/api/v1/notify/settings` | A | — → 200 `NotifySettings` | — |
| PATCH | `/api/v1/notify/settings` | A | writable partial settings → 200 `NotifySettings` | 400 invalid/read-only fields |
| GET | `/api/v1/push/key` | A | — → 200 `{publicKey}` | — |
| POST | `/api/v1/push/subscribe` | A | `PushSubscription` → 204 | 400 endpoint/keys; 409 subscription quota |
| POST | `/api/v1/push/unsubscribe` | A | `{endpoint}` → 204 | 400 endpoint |
| POST | `/api/v1/push/test` | A | — → 204 | 409 no devices; 502 push_failed (retryIn=5 on provider rejection, otherwise absent) |

List limit clamped to inbox retention; unread boolean. Read body accepts ids?
and all?, but the emitted NotificationsRead has required ids and optional all.
Deleting unknown notifications returns 404. NotifyRequest requires title or body;
kind/body/link/sessionId/agent/severity optional. Notify settings writable:
rules, quietStart, quietEnd, ntfyUrl, webhookUrl. devices and vapidKey read-only.
Push key may be empty; capability/key existence does not prove delivery to a
real device. Subscribe endpoint/keys required, device optional. No external
notification delivery is exercised by contract fixtures.

## Clipboard, snippets and notes (internal/clip, internal/snippets)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/clip?limit=50` | A | → 200 `Clip[]` | — |
| POST | `/api/v1/clip` | A | `{text,source?}` → 200 `Clip` | 400 empty; 413 too_large |
| DELETE | `/api/v1/clip/{id}` | A | — → 204 | — |
| DELETE | `/api/v1/clip` | A | — → 204 | — |
| GET | `/api/v1/snippets` | A | → 200 `Snippet[]` | — |
| POST | `/api/v1/snippets` | A | writable snippet fields → 200 `Snippet` | 400 validation; 409 quota |
| GET | `/api/v1/snippets/{id}` | A | — → 200 `Snippet` | 404 missing |
| PATCH | `/api/v1/snippets/{id}` | A | partial writable snippet → 200 `Snippet` | 400 validation; 404 missing |
| DELETE | `/api/v1/snippets/{id}` | A | — → 204 | 404 missing |
| POST | `/api/v1/snippets/{id}/use` | A | — → 200 `Snippet` | 404 missing |
| GET | `/api/v1/notes` | A | → 200 `Note[]` | — |
| POST | `/api/v1/notes` | A | title?/text? → 200 `Note` | 400 validation; 409 quota |
| GET | `/api/v1/notes/{id}` | A | — → 200 `Note` | 404 missing |
| PATCH | `/api/v1/notes/{id}` | A | title?/text? → 200 `Note` | 400 validation; 404 missing |
| DELETE | `/api/v1/notes/{id}` | A | — → 204 | 404 missing |

Clip max text 256 KiB; source defaults web (cli for local), unknown source falls back to web; accepts terminal,
cli, web, osc52, desktop. Lists clamp to retention. Snippet writable fields:
name/body/kind(prompt or command)/tags/agent; id/updatedAt/uses are accepted
by its decoder but ignored. Note id/updatedAt also ignored. Snippet/Note are
response types, not unrestricted writable PATCH contracts. Use increments uses.

## Schedules (internal/schedule)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/schedules` | A | — → 200 `Schedule[]` | — |
| POST | `/api/v1/schedules` | A | writable fields → 200 `Schedule` | 400 validation; 409 quota |
| GET | `/api/v1/schedules/describe?cron=&timezone=&count=3` | A | → 200 `CronPreview` | invalid cron/timezone is 200 valid:false with error |
| GET | `/api/v1/schedules/{id}` | A | — → 200 `Schedule` | 404 missing |
| PATCH | `/api/v1/schedules/{id}` | A | partial writable fields → 200 `Schedule` | 400 validation; 404 missing |
| DELETE | `/api/v1/schedules/{id}` | A | — → 204 | 404 missing |
| POST | `/api/v1/schedules/{id}/run` | A | — → 200 `ScheduleRun` | 404 missing |
| GET | `/api/v1/schedules/{id}/runs?limit=20` | A | → 200 `ScheduleRun[]` | 404 missing |

Writable fields are name/cron/timezone/cwd/agent/prompt/command/mode/enabled/
notify, individually optional in the decoder, with creation/default validation.
id/nextRun/lastRun/createdAt are accepted but ignored. Mode headless/interactive;
cron must be valid, cwd resolves to an existing absolute directory (no workspace-root allowlist here), command or agent prompt required
as appropriate. Describe count clamped 1–10; valid required, description/next/
error optional. Run-now returns the stored initial running record or skipped
for overlap; it is not completion or 202. schedule.run reports finish; exitCode,
terminalId, output and finishedAt optional (zero timestamp rule applies).
Runs limit clamped to retained history. Fixtures do not prove actual scheduling
against installed agents or execution after a service restart.

## Command center (internal/search)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/search?q=&scopes=a,b&limit=8` | A | → 200 `SearchResponse` | 429 rate_limited, retryIn/Retry-After |
| GET | `/api/v1/scripts` | A | — → 200 `ScriptCommand[]` | — |
| POST | `/api/v1/scripts/{id}/run` | A | optional `RunScriptRequest` → 200 `RunScriptResult` | 400 args; 404 missing; 429 slots full (retry 1); 503 terminal unavailable; 504 timeout |
| POST | `/api/v1/ask` | A | `AskRequest` → 200 NDJSON `AskChunk` | 400 prompt/agent/cwd; 429 busy/hourly quota; 503 no headless agent |

Federated search normalizes whitespace, caps query at 200 runes, clamps limit
1–50 **per scope**, default 8; scopes CSV, unknown scopes ignored. Provider
budget 150 ms; late/panicking providers omitted. Admission is a service-wide
20-token/second bucket, burst 40; 429 retry is rounded up, minimum one second.
Scripts are discovered from configured command directories (default user config
commands). args optional; terminal mode returns terminalId and exitCode=0 for
admission, not job exit. Inline/silent nonzero exit still returns 200 with
exitCode; output optional/capped 64 KiB; timeout 30 seconds; four active slots.

Ask validates before admission: prompt required, trimmed, at most 32 KiB, no
NUL; agent/cwd optional; cwd absolute when supplied. Unknown/non-headless agent
is 400; no installed headless candidate is 503. One service-wide in-flight ask
and 20 admissions/hour in memory. 429 busy retry=1; hourly retry is time until
the oldest admission expires, rounded up. Both error retryIn and Retry-After
use seconds. After 200, text/done/error chunks carry t and optional text;
execution failures use error chunks, not new HTTP errors. Raw agent stdout is
split into UTF-8 chunks up to 4 KiB, budget 120 seconds; disconnect cancels the
child. No ask progress events on /events. These checks do not test real providers.

## Toolbox (internal/toolbox)

| Method | Path | Auth | Request → success | Errors |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/toolbox?refresh=` | A | → 200 `Tool[]` | — |
| POST | `/api/v1/toolbox/{id}/install` | A | — → 202 `TerminalSession` | 400 unsupported; 404 unknown; 409 already installing; 503 daemon unavailable |
| GET | `/api/v1/toolbox/mcp` | A | — → 200 `MCPServer[]` | — |
| POST | `/api/v1/toolbox/mcp/apply` | A | `MCPApplyRequest` → 200 `MCPServer` | 400 server/agents/input; 404 vanished server; 409 config conflict; 502 agent_cli_failed |

Install runs visibly in a toolbox terminal; toolbox.job carries the canonical
ToolboxJob lifecycle described above. List caches 60 seconds; refresh boolean
bypasses cache. Apply requires server/agents[], remove?; the response's env and
agents maps are optional/present according to their Go tags. Apply changes
agent configuration; it is not exercised by this ticket's fixtures.

## Backend-only routes and bus contracts

The local Relay control socket serves the **same /api/v1 routes** with a local
principal (not browser cookies). CLI notify/clip/open/preview/hook use those
contracts; open publishes a broadcast event. RELAY_SESSION selects the caller's
terminal for hooks. Do not expose socket paths or private environment values
as examples.

The separate ptyd owner socket (internal/ptyd/server.go) accepts only the owner
UID. These `/v1` routes are backend-only; they are not registered at `/api/v1`
and are not browser types. CwdUsage is also backend-only exchange between
agents and workspaces, with no HTTP route/browser mirror. Owner checks reject
other UIDs with plain-text 403; unmatched paths/methods use 404/405. Handler
errors use ErrorBody; bridge D mapping above applies. All daemon handlers can
return 500 for unexpected failures. Streaming/upgrade can fail before headers,
or terminate after headers. JSON bodies use the shared 1 MiB decoder.

| Surface | Method | Path | Request → success | Errors |
| --- | --- | --- | --- | --- |
| ptyd | GET | `/v1/health` | — → 200 `{ok,version,sessions}` | — |
| ptyd | GET | `/v1/settings` | — → 200 TerminalDefaults plus configId | 400 cwd; 503 defaults unavailable/unsupported |
| ptyd | GET | `/v1/sessions` | — → 200 `TerminalSession[]` | — |
| ptyd | POST | `/v1/sessions` | daemon CreateSpec → 201 `TerminalSession` | 400 decode/kind/argv/meta/cwd/start; 409 session limit; 503 defaults |
| ptyd | GET | `/v1/sessions/{id}` | — → 200 `TerminalSession` | 404 missing |
| ptyd | PATCH | `/v1/sessions/{id}` | name?/pinned?/meta? → 200 `TerminalSession` | 400 decode/meta; 404 missing |
| ptyd | DELETE | `/v1/sessions/{id}` | signal?/forget? query → 204 legacy mutation | 400 signal; 404 missing; 503 forget timeout |
| ptyd | POST | `/v1/sessions/{id}/delete` | DeleteSpec (signal?/forget?) → 200 DeleteResult (changed) | 400 decode/signal; 404 missing; 503 forget timeout |
| ptyd | POST | `/v1/sessions/{id}/input` | raw bytes, paste? query → 204 | 404 missing; 409 exited; 413 input read/size failure |
| ptyd | POST | `/v1/sessions/{id}/resize` | cols/rows → 204 | 400 decode/nonpositive; 404 missing |
| ptyd | POST | `/v1/sessions/{id}/attention` | Attention or null → 204 | 400 decode; 404 missing |
| ptyd | POST | `/v1/sessions/{id}/restore` | — → 201 new `TerminalSession` | 404 missing; 409 running/session limit; 400/503 creation errors |
| ptyd | GET | `/v1/sessions/{id}/snapshot` | lines clamped 1–1500 → 200 `TerminalSnapshot` | 404 missing |
| ptyd | GET | `/v1/sessions/{id}/recording` | — → 200 asciicast bytes | 404 session/recording missing |
| ptyd | GET | `/v1/sessions/{id}/attach` | cols?/rows?/replay?/readonly? → 101 terminal binary/text socket | 404 missing; handshake 400/403 |
| ptyd | GET | `/v1/events` | — → 101 daemon event WebSocket (JSON text) | handshake 400/403 |

CreateSpec embeds CreateTerminalRequest and adds backend-only ExtraEnv,
Scrollback, AgentSessionID and Workspace (Go field names, without JSON tags);
these default to nil/zero/empty when absent. PATCH metadata merges; empty
values delete keys. Attention null clears it; missing reason defaults to hook.
Raw input is capped at 1 MiB. Resize clamps valid positive sizes and returns
204 even when the underlying resize cannot apply. Snapshot's daemon clamp
also applies after the public route's 1–5000 query clamp.

Daemon attach uses the terminal frame protocol above with a 1 MiB + 1024
read limit, 30-second writes, replay unless exactly 0 and its own ACK window/
output queue. Read-only clients cannot input or resize. Its normal exit closes
1000, lagging 1013, other failures abruptly; it has no browser credential guard.
The daemon event socket is a different protocol from the browser Event socket:
PtyEvent has type(created/updated/exited/removed/notify/bell/clip/open), optional
session/id/title/body/text/path. It has no browser Event.at/data envelope,
hello, snapshot or replay. A 512-event subscriber queue avoids blocking session
producers; a dropped subscriber drains and closes 1001. Writes time out after
30 seconds; CloseRead handles control frames and rejects application input.
See [PTYD.md](PTYD.md) for peer authentication, frame limits and persisted
terminal state; public types are adapted by internal/ptyclient and terminal.

| Backend bus topic | Canonical contract / consumer |
| --- | --- |
| `audit` | `core.AuditEvent`; auth records after successful sensitive mutations (legacy api.AuditEntry also accepted) |
| `clip.capture` | `core.ClipCapture`; clip validates/stores text, then emits public clip |
| `auth.session.revoked` | `core.SessionRevoked` with public SessionIDs/TokenIDs; auth emits after successful persistence; SocketGuard treats it as a revalidation hint |

All three are excluded from live forwarding by core.IsBackendTopic and have
no browser EventType. Revocation hints contain no credentials or hashes; lost
hints, offline writers and expiry are covered by uncached socket polling,
not by assuming reliable bus delivery. See [ARCHITECTURE.md](ARCHITECTURE.md#cross-feature-events)
and [AUTH.md](AUTH.md#revocation-ownership).

## Terminal mutation audit

Confirmed terminal kill/forget and upload complete/cancel each publish one
audit event with the affected public ID and optional whitelisted signal.
Denials, failures, staging chunks and retries of consumed mutations imply no
new success. Audit detail excludes commands, names, file bodies, prompts and
credentials. See [the mutation/retry contract](PTYD.md#terminal-mutation-audit),
including private daemon confirmation and asynchronous delivery limits.
