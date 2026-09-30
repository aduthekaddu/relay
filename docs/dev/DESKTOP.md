# Apps and desktop

`internal/apps` runs on-demand apps behind Relay's login: the browser IDE,
user-defined `[[apps]]`, and the remote desktop. Each one is a supervised
child in its own process group (`Setpgid`, `Pdeathsig` on Linux). It is
started on first use, stopped with SIGTERM then SIGKILL on the whole
group, and stopped again after its idle timeout (checked every 30 s).
Nothing restarts on its own; the next request starts it again. State
changes publish `app.state` (and `desktop.state`).

## Code (browser IDE)

Binary: `code.binary`, then `code-server` / `openvscode-server` on PATH,
then `~/.local/bin`, then the newest `~/.local/lib/code-server-*`. It runs
fully separate from any other code-server on the machine:

```
code-server --socket <runtime>/code.sock --auth none --disable-telemetry
  --disable-update-check --user-data-dir <data>/code/user
  --extensions-dir <data>/code/extensions --abs-proxy-base-path /apps/code
```

`--auth none` is safe because the socket lives in the 0700 runtime
directory and every request comes through `/apps/code/`. That route uses
`rt.Authenticate`, requires same-origin for cookie-authenticated unsafe
requests and WebSockets, and strips Relay credentials. The first
navigation shows a "Starting Code..." page that reloads itself. Other
requests get 503 with `Retry-After`. `?folder=` passes through.
`code.idle_stop` (default 2 h) stops it after no traffic.

## User apps

```toml
[[apps]]
id = "grafana"          # lowercase, not code/desktop -> /apps/grafana/
name = "Grafana"
port = 3001             # loopback port, or: socket = "grafana.sock"
command = ["grafana", "server"]   # optional: Relay starts/stops it
env = { GF_SERVER_ROOT_URL = "/apps/grafana/" }
idle_stop = "1h"
```

Children get `RELAY_BASE_PATH`, plus `PORT` or `RELAY_APP_SOCKET`.
Relative sockets live in the runtime directory. Without `command`, Relay
only proxies, and the app counts as running while it accepts connections.

## Desktop

TigerVNC `Xvnc` on `desktop.display` (default `:7`), `desktop.geometry`
(default 1600x1000):

```
Xvnc :7 -geometry WxH -depth 24 -rfbport -1 -rfbunixpath <runtime>/vnc.sock
  -rfbunixmode 0600 -SecurityTypes None -localhost -nolisten tcp
  -auth <runtime>/desktop/Xauthority -AlwaysShared -AcceptSetDesktopSize
```

RFB has no TCP listener at all. It is reachable only through the 0600 unix
socket and Relay's authenticated WebSocket bridge. X11 uses a fresh
MIT-MAGIC-COOKIE per start. Relay refuses to start if another X server
owns the display, and removes its own stale sockets and locks.

The session is `deploy/desktop/` (embedded in the binary), rendered into
`<runtime>/desktop/` on each start. The user's own openbox/tint2 configs
are never read:

- `xstartup`: `xsetroot -solid '#0b0b0c'`, no blanking, tint2 (restarted
  if it crashes), then `exec openbox`. It runs under `dbus-run-session`
  when available. The desktop ends when openbox exits.
- `openbox/rc.xml` plus the `Relay` theme (carbon/bone, orange hairline on
  the focused window). Keys: Super+Left/Right snap to halves, Super+Up
  maximizes, Super+Down restores, Super+Return opens a terminal,
  Super+E opens files, Super+B opens Chrome, Super+D shows the desktop,
  Alt+Tab switches windows, Alt+F4 closes. Right-click the desktop for the
  app menu.
- `tint2rc`: bottom panel with the launcher, taskbar and clock.

The catalog lists installed apps only: Chrome/Chromium (with
`--user-data-dir=<data>/desktop/chrome --no-first-run
--no-default-browser-check`), Blender, a terminal (xterm, then
x-terminal-emulator, ...), a file manager (thunar, pcmanfm, nautilus, ...)
and `[[desktop.apps]]` entries, which can override a built-in by id. Every
launch path (panel, menu, keys, API) goes through `<runtime>/desktop/bin/<id>`.
That wrapper sets `RELAY_DESKTOP_APP=<id>`, which is how `apps[].running`
is detected. On stop, marked processes that outlive X are terminated. The
session forces X11 toolkits and disables the accessibility bus and
portals, because GTK apps otherwise hang in this bare session.

Idle stop: `desktop.idle_stop` (default 2 h) with no connected viewers.

| Method | Path | |
| --- | --- | --- |
| GET | `/api/v1/desktop` | `DesktopState` |
| POST | `/api/v1/desktop/start` / `stop` | `DesktopState` |
| POST | `/api/v1/desktop/launch` | `{app}`, starts the desktop if needed |
| GET / POST | `/api/v1/desktop/clipboard` | `{text}` (xclip, <= 1 MiB; POST sets CLIPBOARD and PRIMARY) |
| POST | `/api/v1/desktop/resize` | `{width, height}` via xrandr (modes created on the fly) |
| GET (WS) | `/api/v1/desktop/ws` | binary RFB (subprotocol `binary`, noVNC) |

The WS bridge starts the desktop on demand. It copies frames both ways
with natural backpressure, pings every 25 s, and allows up to 8 viewers.
When the desktop stops, it closes with 1001.

Install on Debian/Ubuntu:
`sudo apt-get install --no-install-recommends tigervnc-standalone-server openbox tint2 xclip x11-xserver-utils`
(`xdotool` optional). macOS: unavailable.

Agents can drive the same display with `DISPLAY=:7` and
`XAUTHORITY=<runtime>/desktop/Xauthority`.
