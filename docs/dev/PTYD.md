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
| POST | `/v1/sessions/{id}/input` | raw bytes body |
| POST | `/v1/sessions/{id}/resize` | `{cols, rows}` |
| POST | `/v1/sessions/{id}/attention` | `api.Attention` JSON or `null` to clear |
| GET | `/v1/sessions/{id}/snapshot?lines=` | `api.TerminalSnapshot` |
| GET | `/v1/sessions/{id}/recording` | asciicast v2 stream |
| GET (WS) | `/v1/sessions/{id}/attach?cols=&rows=&replay=1&readonly=0` | same frames as the browser protocol (API.md) |
| GET (WS) | `/v1/events` | `ptyclient.PtyEvent` JSON text frames |

`relay serve` bridges browser WebSockets to the attach socket frame by
frame (it can enforce read-only and inspect control messages).

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
- **Screen model** for snapshots and previews: a lightweight VT emulator
  (evaluate `github.com/charmbracelet/x/vt`; otherwise a minimal
  in-house one handling printable text, CR/LF/BS/TAB, CSI cursor moves,
  ED/EL, scroll regions, IL/DL/ICH/DCH, alt screen). `Preview` = last 3
  non-empty lines.
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

## Tests

Real PTYs with `/bin/sh -c`: spawn, echo round-trip, resize, kill codes,
replay correctness (including a truncated buffer and alternate screen),
OSC parsing table tests, attention transitions, recorder format, restart
of the daemon keeping metadata.
