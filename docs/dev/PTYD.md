# ptyd — the session daemon

`relay ptyd` owns every terminal. It runs as its own systemd user unit
(`relay-ptyd.service`, `KillMode=process` so sessions keep their own
process groups) and listens on `$RUNTIME/ptyd.sock` (0600, dir 0700).
`relay serve` connects as a client (`internal/ptyclient`). Service ownership
is explicit. Rendered systemd serve units and launchd serve agents set
`RELAY_NO_PTYD=1`, so only the separate daemon service starts ptyd.
Systemd requests ptyd with `Wants` and orders serve after it with `After`.
`Type=simple` orders process startup, not socket readiness. Launchd has no
readiness ordering between these agents. Serve stays available while the
daemon starts or is missing. Terminal operations return 503 and the event
relay reconnects when the daemon becomes available.

A direct `relay serve` with `RELAY_NO_PTYD` unset or different from `1`
checks daemon health and starts ptyd detached with `setsid` when needed,
even if systemd is available. It reuses a healthy daemon. Stopping serve
never stops ptyd. When a service or a manually started foreground daemon
owns ptyd, use `RELAY_NO_PTYD=1 relay serve` to retain that ownership.
If the daemon unit is missing, repair it with `relay setup`; managed serve
does not silently replace it with a child. For an intentional direct-run
fallback, use `env -u RELAY_NO_PTYD relay serve`. Keep the same Relay paths
for both processes. `relay run` also ensures a daemon independently.

Do not start a daemon service over a detached fallback that already holds
the lock. To transfer ownership, first finish its PTYs and stop that daemon
by its known PID, then start the service. Restarting ptyd ends its sessions.

## Protocol (HTTP over the unix socket)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/health` | `{ok, version, sessions}` |
| GET | `/v1/settings` | Next terminal defaults, file/startup source and private config identity |
| GET | `/v1/sessions` | `[]api.TerminalSession` |
| POST | `/v1/sessions` | `ptyclient.CreateSpec` → session |
| GET/PATCH/DELETE | `/v1/sessions/{id}` | get / update name, pinned, meta / kill (`?signal=`) `&forget=1` |
| POST | `/v1/sessions/{id}/restore` | new session with the command, cwd and env of an exited one (`meta.restoredFrom`) |
| POST | `/v1/sessions/{id}/input` | raw bytes body (`?paste=1`: bracketed paste when enabled) |
| POST | `/v1/sessions/{id}/resize` | `{cols, rows}` |
| POST | `/v1/sessions/{id}/attention` | `api.Attention` JSON or `null` to clear |
| GET | `/v1/sessions/{id}/snapshot?lines=` | `api.TerminalSnapshot` |
| GET | `/v1/sessions/{id}/recording` | asciicast v2 stream |
| GET (WS) | `/v1/sessions/{id}/attach?cols=&rows=&replay=1&readonly=0` | same frames as the browser protocol (API.md) |
| GET (WS) | `/v1/events` | `ptyclient.PtyEvent` JSON text frames |

`relay serve` bridges browser WebSockets to the attach socket frame by
frame (it can enforce read-only and inspect control messages).

The daemon holds `$RUNTIME/ptyd.lock` (flock, contains its pid) for its
lifetime; a second `relay ptyd` exits with "already running".
`ptyclient.EnsureDaemon` probes health, then the lock, and only then
starts `relay ptyd --socket …` with `setsid`, stdio to
`$DATA/ptyd/ptyd.log` (rotated at 10 MiB), waiting up to 5 s. Concurrent
callers are safe: losers of the lock race exit on their own.
`relay serve` calls it at startup unless `RELAY_NO_PTYD=1` (set that when
`relay-ptyd.service` owns the daemon).

The CLI daemon reloads shell, default cwd and recording from its own config
file for each new creation. The three defaults come from one snapshot;
explicit session overrides still win. Existing sessions are unchanged.
Other daemon settings and its cached login environment remain startup
state. Serve compares the source identity and reports older or differently
configured daemons explicitly. No settings save restarts a service. See
[runtime settings](RUNTIME_SETTINGS.md).

## Sessions

- Spawn with `creack/pty` in a new session/process group, cwd validated,
  env = daemon env (from the user's login environment) + spec env +
  `RELAY_SESSION=<id>`, `RELAY_SOCKET=<relay.sock>`, `TERM=xterm-256color`,
  `COLORTERM=truecolor`, `TERM_PROGRAM=Relay`, `TERM_PROGRAM_VERSION`,
  `LANG` default `C.UTF-8` if unset, `DISPLAY` when the desktop runs.
- Default command: the user's login shell (`$SHELL -l`).
- Ids: `t_` + 10 base32 chars. Names default to the command basename or
  the agent name, then follow OSC titles unless renamed.
- Session metadata persists in `$DATA/ptyd/sessions.json` so exited
  sessions (and the specs of sessions lost to a reboot) can be listed and
  restarted ("Restore" in the UI).
- Kill: SIGHUP to the process group, then SIGTERM after 2 s, SIGKILL
  after 5 s. Exit code recorded.

## Output pipeline (per session)

```
pty master ─▶ reader goroutine ─▶ parser (OSC/DEC modes/BEL) ─▶ ring buffer (2 MiB)
                                            │                    └▶ recorder (asciicast)
                                            └▶ events (title, cwd, notify, bell, clip)
                                  └▶ fan-out to attached clients (per-client bounded queue)
```

- **Ring buffer** of raw bytes (configurable). Replay starts at a safe
  boundary (after a newline, outside an escape sequence).
- **Mode tracker**: remembers alternate screen (1049/1047/47), mouse modes
  (1000/1002/1003/1005/1006/1015), bracketed paste (2004), application
  cursor keys (1), cursor visibility (25), focus reporting (1004),
  synchronized output (2026). On attach: send `ESC c`, replay the buffer,
  then re-assert the tracked modes; if the alternate screen is active,
  nudge the size (rows−1 then rows) so full-screen apps redraw.
  Attach sends `hello` (session, size, readOnly), then `replay-begin`,
  the bytes, `replay-end`, then `clients`.
- **Screen model** for snapshots and previews: a compact in-house VT
  emulator (`screen.go` + `vtparse.go`) handling printable text with
  wide runes, CR/LF/BS/TAB, CSI cursor moves, ED/EL, scroll regions,
  IL/DL/ICH/DCH/ECH, save/restore cursor, alt screen, and a bounded
  scrollback of plain-text lines. `Preview` = last 3 non-empty lines.
  `charmbracelet/x/vt` was evaluated and not used: untagged/pre-1.0 with
  API churn, a larger dependency tree, and per-cell style storage we do
  not need (snapshots are text only), which costs several times the memory
  per session.
- **OSC handling**: 0/2 title; 7 cwd (`file://host/path`); 9 (iTerm2)
  and 777;notify (rxvt) → notify event; 99 (kitty) basic; 52 → clip event
  (base64 payload, size-capped); 1337;SetUserVar ignored. OSC 133
  (shell integration) prompt marks → "prompt" boundaries for activity.
- **BEL** outside OSC → bell event (rate-limited).
- **Recording**: asciicast v2 (`{"version":2,"width","height","timestamp","env"}`
  then `[t, "o", data]` / `[t, "r", "COLSxROWS"]`), gzip-rotated, retention
  from config.

## Activity & attention

- `working`: output within the last 1.5 s (spinners count).
- `idle`: no output for `idle_seconds` (default 8) and no attention.
- `waiting` (attention): explicit signal — hook (`relay hook` → server →
  ptyd `attention`), OSC 9/777, BEL from an agent session, or the prompt
  heuristic for agent sessions (output stopped and the last screen line
  matches a known input prompt pattern for that agent). Cleared on user
  input or explicit ack.
- Every change emits `updated` on `/v1/events`.

## Clients

- Multiple clients per session. Size policy "latest active wins".
- Per-client send queue bounded (1 MiB); a client that falls behind gets
  `{"t":"error","message":"lagging"}` and is disconnected (the browser
  reconnects and replays).
- Read-only clients never write input or resize.

## Web API front (`internal/terminal`)

- Routes per API.md plus two additive ones: `POST
  /api/v1/terminals/{id}/restore` → `TerminalSession` (201) and `GET
  /api/v1/uploads/{id}` → `Upload` (to resume after a reload).
  `?download=1` on the recording adds `Content-Disposition`.
- ptyd errors map to HTTP: unknown session 404, daemon down 503
  (`unavailable`), daemon validation errors keep their status and message.
- Attach bridge: ptyd is dialled *before* the upgrade (so 404/503 are real
  HTTP answers); frames are copied unchanged towards the browser (acks are
  end to end, so ptyd's 1 MiB window applies to the browser); browser text
  frames are re-encoded as `TermClientMsg` with only `resize` (1–1000 ×
  1–500), `focus`, `ack`, `ping` passed; `?readonly=1` drops input and
  resizes in the bridge *and* attaches read-only to ptyd. The bridge pings
  the browser every 30 s. A ptyd close status (`exited`, `lagging`) is
  forwarded; an unexpected daemon drop closes with 1013 (retry).
- tmux import (`terminal.import_tmux`): `GET /terminals` appends each tmux
  session not already open in a live Relay session as `kind:"tmux"`,
  `id:"tmux:<name>"`, `meta.importable="1"`, `meta.tmux=<name>`. `POST
  /terminals {kind:"tmux", meta:{tmux:<name>}}` (or `name`) creates a ptyd
  session running `tmux attach-session -t =<name>` (exact match) in the
  tmux session's path, or returns the live one that already has it.
  `TMUX` is stripped from the environment used for tmux queries.
- Events: ptyd `created/updated/exited/removed` → `api.EvTerminal*`; after
  every (re)connect all sessions are re-published as `terminal.updated`.
  `notify`/`bell` from `kind:"agent"` sessions → `Notifier` (`kind:
  "attention"`, link `/terminal/<id>`); OSC 52 `clip` → backend topic
  `clip.capture`. The relay reconnects with 0.5 s → 10 s backoff.
- Uploads: partials live in `$DATA/uploads/.partial/<id>.part` + `.json`
  (0600), so they survive restarts. Chunks are ≤ 4 MiB; `PUT ?offset=N`
  accepts any offset ≤ received (a retried chunk overwrites) and refuses
  gaps with 409 `offset_mismatch`. Size is capped by `upload_max_mb` at
  start (413) and on every chunk; free disk space is checked at start;
  at most 64 unfinished uploads; starts are rate-limited (60/min).
  Completion hard-links (or `O_EXCL`-copies across filesystems) into
  `$DATA/uploads/YYYY-MM-DD/` (0600) or into `dir` (0644), which must
  resolve — symlinks followed, re-checked at completion — inside
  `files.root`. Names are sanitised (base name only, no control or bidi
  characters, no leading dots/dashes, ≤ 200 bytes keeping the extension)
  and never overwrite: `name (1).ext`, `name (2).ext`, … Partials with no
  writes for 24 h are swept hourly.

## CLI

- `relay ptyd [--socket path] [--debug]` runs the daemon in the foreground.
- `relay ls [--json] [--live]` talks to ptyd directly (works without
  `relay serve`): ID, NAME, STATUS (working/idle/waiting/exited(code)),
  CWD, CLIENTS, AGE.
- `relay attach [--readonly] [--no-replay] <id|name|id-prefix>`: raw mode
  via `x/term`, replay on attach, resize on SIGWINCH, acks every 64 KiB,
  detach with `Ctrl+\` then `d` (`Ctrl+\` twice sends one). Exits with
  the session's exit code when it ends; restores the local terminal
  (main screen, cursor, mouse/paste modes off).
- `relay run [--name n] [--cwd d] [--attach] [--json] -- cmd args…` starts
  a `kind:"task"` session (ensuring the daemon), passing the caller's
  `PATH` so commands resolve as in the calling shell.

## Tests

Real PTYs with `/bin/sh -c`: spawn, echo round-trip, resize, kill codes,
replay correctness (including a truncated buffer and alternate screen),
OSC parsing table tests, attention transitions, recorder format, restart
of the daemon keeping metadata.

## Terminal mutation audit

Terminal handlers publish `terminal.kill`, `terminal.forget`, `upload.complete`
and `upload.cancel` after the mutation owner confirms success. Detail contains
the affected public terminal or upload ID and, for a kill, the whitelisted
signal name. IP contains a parsed address or `local`; arbitrary proxy-header
text is excluded. Terminal names, commands, environment, uploaded names, paths,
types, file bodies, prompts and credentials
are excluded. Failures, authorization/CSRF denials, upload start/chunk/status,
and stale staging cleanup produce no success entry.

Default close is HUP. Repeated HUP, TERM, KILL or QUIT deliveries to a session
are no-ops after the first confirmed delivery of that signal. A different
terminating signal is still delivered, so TERM can follow HUP immediately.
HUP/TERM share one escalation lifecycle. An exited session is a no-op.
INT/USR1/USR2 are separate deliveries, so intentionally repeated interrupts
remain usable.

Concurrent forgets remove the record once. A live forget produces one
`terminal.forget`, without an extra kill event. Consumed upload IDs return 404
on complete/cancel retry, including after the upload service is reconstructed.
Partial and resumed uploads produce `upload.complete` only when the final file
is placed. Explicit cancellation produces `upload.cancel` once, after cleanup.

The private `POST /v1/sessions/{id}/delete` endpoint accepts `{signal,forget}`
and returns `{changed}`. Audited terminal deletion requires a daemon supporting
this endpoint. Older daemons reject it with 404 before mutation. Compatibility
daemon DELETE and client Kill/Remove wrappers do not publish audit themselves.
No automatic service restart is part of this contract.

The existing asynchronous audit bus and SQLite consumer remain unchanged.
These guarantees cover acknowledged operations with a healthy audit consumer;
they do not add durable delivery during bus overload, process crashes, or a
lost daemon response after a signal was delivered.
