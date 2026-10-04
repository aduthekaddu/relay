# Relay architecture

Relay turns one Linux (or macOS) machine into a private workspace you can
open from any browser: persistent terminals, every coding-agent session,
files, previews of dev servers, a browser IDE and a full remote desktop,
behind one login. It ships as **one Go binary** with the web app embedded.

```
 browser / phone (PWA)                       the machine
┌──────────────────────┐   HTTPS/WSS   ┌───────────────────────────────────────────┐
│ Preact app           │ ────────────▶ │ relay serve  (web server, API, proxies)   │
│  xterm.js, CM6, noVNC│               │   ├─ auth (password, passkeys, TOTP)      │
└──────────────────────┘               │   ├─ /api/v1/*  JSON + WebSocket          │
                                       │   ├─ /apps/code/*  → code-server (socket) │
 relay CLI inside a terminal           │   ├─ /api/v1/desktop/ws → Xvnc (socket)   │
┌──────────────────────┐  unix socket  │   ├─ previews  → 127.0.0.1:<port>         │
│ relay notify/clip/   │ ────────────▶ │   └─ SQLite relay.db                      │
│ open/attach/hook     │  relay.sock   │                                           │
└──────────────────────┘               │ relay ptyd  (session daemon, own unit)    │
                                       │   owns every PTY: shells, agents, tasks   │
                                       │   ring buffer replay, OSC parsing,        │
                                       │   activity + attention detection          │
                                       └───────────────────────────────────────────┘
```

Two processes, one binary:

| Process | Unit | Owns | Survives |
| --- | --- | --- | --- |
| `relay serve` | `relay.service` | HTTP(S), auth, API, proxies, indexing, schedules | — |
| `relay ptyd` | `relay-ptyd.service` | every terminal process and its scrollback | `relay serve` restarts and upgrades |

Everything the user runs lives under `ptyd`, so upgrading or restarting the
web server never kills a shell or an agent in the middle of a task.

## Source layout

```
cmd/relay/            main: dispatches to internal/cli
internal/
  api/                JSON contract types (mirrored in web/src/api/types.ts)
  app/                wiring: Build(), Run(), one wire_<feature>.go per feature
  cli/                command registry; cmd_<name>.go per subcommand
  config/             relay.toml, defaults, paths (XDG / RELAY_HOME)
  core/               Deps container + cross-feature interfaces
  events/             in-process pub/sub bus
  httpx/              JSON/error helpers
  server/             router (auth levels, CSRF), listeners, TLS, headers
  store/              SQLite + per-feature migrations
  web/                embedded SPA (dist/ is built from /web)
  deps/               blank imports pinning third-party modules
  <feature>/          auth, live, notify, workspaces, agents, terminal, files,
                      system, previews, apps, clip, snippets, schedule,
                      search, toolbox, info, ptyd, ptyclient
web/                  Preact + TypeScript app (Vite)
site/                 marketing site + documentation (Astro + Starlight)
scripts/              install.sh (curl | bash), dev helpers, release
deploy/systemd/       unit templates (also embedded for `relay setup`)
docs/                 user documentation (rendered by the site)
docs/dev/             contributor documentation (this folder)
```

## Feature package contract

Every feature is a package under `internal/` that exposes:

```go
func New(d *core.Deps) (*Service, error)     // construct; no goroutines
func (s *Service) Routes(rt *server.Router)  // register HTTP routes
func (s *Service) Start(ctx context.Context) error // optional background loop; return when ctx is done
func (s *Service) Close() error               // optional
```

and is wired in `internal/app/wire_<feature>.go`, which the feature owns:

```go
func wireFiles(ctx context.Context, a *App) error {
	svc, err := files.New(a.D)
	if err != nil { return err }
	svc.Routes(a.Router)
	a.OnStart("files", svc.Start)   // if it has a loop
	a.OnClose(svc.Close)            // if it holds resources
	a.D.Search.Add(svc.SearchProvider()) // if it contributes to ⌘K
	return nil
}
```

Rules:

- A feature talks to another feature only through `core.Deps` interfaces
  (`Notifier`, `AgentService`, `WorkspaceService`, `SearchRegistry`) or the
  event bus. Never import another feature package (except `ptyclient`).
- Features own their SQLite tables and migrate them with
  `d.Store.Migrate(ctx, "<feature>", []string{...})` (append-only).
- Handlers use `httpx.OK / httpx.Fail / httpx.Decode`. Service errors are
  `*httpx.Err` values (`httpx.NotFound("…")`) so status codes stay correct.
- Background copies, git push/pull, reindex and toolbox installs return 202.
  Toolbox returns the visible TerminalSession and publishes ToolboxJob;
  file copies return FileJob. App starts return 200 with lifecycle state;
  schedule run-now returns 200 with a running or skipped ScheduleRun.
  Search/ask and recording downloads stream after their initial status.
  See [the registered route contracts](API.md) for each response and failure.
- All filesystem paths from clients are cleaned and validated; see
  `docs/dev/SECURITY.md`.

## Cross-feature events

Besides the `api.Ev*` events streamed to browsers, backend-only bus
events connect features without imports:

- `core.BusAudit` (`core.AuditEvent`) — publish after destructive or
  security-relevant actions (file delete, process kill, git discard, hook
  install). `internal/auth` records them in the audit log.
- `core.BusSessionRevoked` (`core.SessionRevoked`) carries public `SessionIDs`
  or `TokenIDs` after auth HTTP handlers commit revocation. `SocketGuard`
  uses the event as a revalidation hint for API WebSockets. CLI/offline
  writers and expiry are detected by uncached polling. No credential values
  or hashes belong in the event. See [the socket bounds](AUTH.md#sessions-and-cookies).
- `core.BusClipCapture` (`core.ClipCapture`) — publish when text arrives
  for the universal clipboard from a non-HTTP source (OSC 52 in a
  terminal, the desktop clipboard). `internal/clip` stores it and emits
  `api.EvClip`.

The topic strings are `audit`, `auth.session.revoked` and `clip.capture`.
`core.IsBackendTopic` excludes all three from live browser forwarding,
including the revocation hint; they do not belong in the browser event union.
Auth publishes revocation only after persistence succeeds. A hint is not a
durable delivery acknowledgement: SocketGuard also checks uncached state at
most every 3 seconds, rejects input after invalidation, and closes with 1008
and the fixed reason `authentication ended` (or abruptly for an unresponsive
peer). Local principals are exempt. Do not put credentials or hashes in hints.

`internal/api/types.go` and feature supplements define public JSON fields;
their counterparts live in `web/src/api`. `events.ts` maps known public topics
to those types, including FileJob and ToolboxJob. The generic Event/RelayEvent
envelope keeps string topics and optional data: unknown future events still
reach wildcard subscribers unchanged. Typed handlers describe canonical
producers; the decoder does not validate arbitrary payloads. Nil Go slices
may encode as null, and value time.Time fields with omitempty still encode a
zero timestamp. Contract reconciliation preserves that current encoding.

Services set on `core.Deps` during wiring (nil-check before use):
`Notifier` (notify), `Workspaces` (workspaces), `Agents` (agents),
`Presence` (live), `Search` registry (always present).

Runtime settings use a shared `config.Runtime` on `core.Deps`, initialized
by app wiring before services start. `Deps.Cfg` is immutable startup state.
Features obtain defensive snapshots; settings writes serialize merge,
persistence and publication. ptyd reloads terminal defaults per creation
and reports its separate effective state through its owner socket. See
[the settings contract](RUNTIME_SETTINGS.md).

## Routing and authentication

`server.Router` has four registration levels:

| Method | Auth | CSRF check | Use for |
| --- | --- | --- | --- |
| `rt.Public` | none | none | login, health, manifest |
| `rt.Handle` | cookie or token or local socket | unsafe methods with cookie require allowed `Origin` | JSON API |
| `rt.WS` | same | cookie upgrades always require allowed `Origin` | WebSockets |
| `rt.Raw` | handler decides (`rt.Authenticate(r)`) | handler decides | SPA, proxies |

Principals: `cookie` (browser session), `token` (`Authorization: Bearer
rly_…`), `local` (the owner-only unix socket `relay.sock`, used by the CLI
from inside terminals — peer uid verified with SO_PEERCRED).

## Data

- `relay.db` (SQLite, WAL) in the data dir: sessions, passkeys, tokens,
  audit log, agent index + FTS, notifications, push subscriptions, clips,
  snippets, notes, schedules, preview labels, settings.
- Agent transcripts are **read, never written** (except hook config files
  the user explicitly asked Relay to install).
- Recordings (asciicast v2) and uploads live in the data dir.
- `relay.toml` (0600) is the only config file.

## Development workflow

```
make dev-go          # run relay serve + relay ptyd against RELAY_HOME=~/.relay-dev on 127.0.0.1:47700
make web-dev         # vite on :5173 proxying /api to :47700
make test            # go test ./... + web unit tests
make build           # web build + go build → bin/relay
make check           # gofmt, go vet, tsc, biome, tests (run before every commit)
```

Ports and paths reserved for development on shared machines:

- `127.0.0.1:47700-47799` for dev servers, `RELAY_HOME=~/.relay-dev*` for state.
- Never bind 80/443 or touch other services on the machine during development.

Heavy commands on small machines should run through `scripts/dev/safe`,
which executes inside a memory-capped, low-priority systemd scope so a
runaway build cannot starve the rest of the system.

## Testing

- Unit tests next to the code (`*_test.go`), table-driven, no network.
- Use `store.OpenMemory()` and `httptest` for handlers; construct a
  `core.Deps` with the fields you need.
- Fixtures must be **synthetic**. Never commit anyone's real transcripts,
  tokens, hostnames or paths.
- `web/`: vitest for pure logic (key mapping, parsers, ranking), plus the
  `/dev/ui` kitchen-sink page for visual review.
- End-to-end: Playwright scripts under `web/e2e/` drive a dev instance.
