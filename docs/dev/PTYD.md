# ptyd — the session daemon

`relay ptyd` owns every terminal. It runs as its own systemd user unit
(`relay-ptyd.service`, `KillMode=process` so sessions keep their own
process groups) and listens on `$RUNTIME/ptyd.sock` (0600, dir 0700).
`relay serve` connects as a client (`internal/ptyclient`). When ptyd is not
running and systemd is unavailable, `relay serve` starts it detached
(`setsid`) — so `relay serve` alone works on a laptop.

## Protocol (HTTP over the unix socket)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/health` | `{ok, version, sessions}` |
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
