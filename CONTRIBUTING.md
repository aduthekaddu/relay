# Contributing to Relay

Thanks for helping. Relay is a single Go binary with an embedded Preact web
app, a marketing site and these docs. This guide gets you from a fresh
clone to a merged pull request: setting up, finding your way around, the
checks every change must pass, and step-by-step recipes for the most common
contributions.

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Set up a development environment

You need:

| Tool | Version | Used for |
| --- | --- | --- |
| Go | 1.27 or newer | Server, session daemon and CLI |
| Node.js | 22 LTS or newer | Web app and site |
| pnpm | 10 or newer (`corepack enable`) | JavaScript dependencies |
| git | any recent | |
| Optional: ripgrep, tmux, TigerVNC, code-server | | Exercising the features that use them |

```bash
git clone https://github.com/aduthekaddu/relay.git
cd relay
pnpm --dir web install
make build            # web build + go build → bin/relay
bin/relay version
```

### Run a development instance

Development instances use an isolated `RELAY_HOME` and a loopback port, so
they never touch a real installation on the same machine:

```bash
make dev-go           # relay ptyd + relay serve on http://127.0.0.1:47700, RELAY_HOME=~/.relay-dev
make web-dev          # Vite dev server (http://127.0.0.1:47780) proxying /api to :47700
```

Open the Vite URL. On first visit you create a local account. To run a
second instance next to the first, use
`PORT=47710 NAME=alt scripts/dev/run.sh`.

To work on the UI without a backend, run the web app against synthetic mock
data:

```bash
cd web && VITE_MOCK=1 pnpm dev
```

### Rules for shared machines

Many contributors develop on a machine that also runs other things (often
their own Relay). The [working agreement](AGENTS.md) applies to humans and
coding agents alike:

- Use loopback ports `47700–47799` and `RELAY_HOME=~/.relay-<name>`. Never
  bind 80/443, and never stop or reconfigure services you did not start.
- Run heavy commands (`go test ./...`, builds, `pnpm install`) through
  `scripts/dev/safe`, which caps memory and lowers CPU priority. The `make`
  targets already do this.
- Kill the processes you started by PID. Don't use broad `pkill -f`
  patterns.
- Never commit secrets, real transcripts, real hostnames, IPs or personal
  paths. Test fixtures are synthetic.

## Find your way around

```text
cmd/relay/            main: dispatches to internal/cli
internal/
  api/                JSON contract types (mirrored in web/src/api/*.ts)
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
scripts/              install.sh, dev helpers, release
deploy/               systemd units, launchd plists, desktop session files
docs/                 user documentation (rendered by the site)
docs/dev/             contributor documentation and specs
```

Read these before changing an area:

| Doc | Covers |
| --- | --- |
| [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md) | Processes, the feature package contract, routing and auth levels, data |
| [docs/dev/API.md](docs/dev/API.md) | Every endpoint and the WebSocket protocols |
| [docs/dev/FEATURES.md](docs/dev/FEATURES.md) | The v1 scope, with acceptance criteria |
| [docs/dev/SECURITY.md](docs/dev/SECURITY.md) | The threat model, and rules every change must follow |
| [docs/dev/DESIGN.md](docs/dev/DESIGN.md) | The "Signal" design system: tokens, type, components |
| [docs/dev/TERMINAL_UX.md](docs/dev/TERMINAL_UX.md) | The mobile terminal and the keyboard translation |
| [docs/dev/PTYD.md](docs/dev/PTYD.md) | The session daemon |
| [docs/dev/SITE.md](docs/dev/SITE.md) | The marketing site |

Area-specific notes (agent adapters, toolbox, previews, desktop,
notifications, command center, UI kit, release) live next to these in
`docs/dev/`.

## `make` targets

| Target | What it does |
| --- | --- |
| `make build` | `make web` + `make go` → `bin/relay` with the web app embedded |
| `make web` | Build the web app into `internal/web/dist` |
| `make go` | Build `bin/relay` with version ldflags |
| `make test` | `test-go` + `test-web` |
| `make test-go` | `go test ./...` |
| `make test-web` | vitest |
| `make fmt` | `gofmt -w` and `biome format` |
| `make vet` | `go vet ./...` |
| `make lint` | gofmt check, `tsc --noEmit`, biome |
| `make check` | `lint` + `vet` + `test`. **Must pass before every commit** |
| `make dev-go` | Development instance on `127.0.0.1:47700` |
| `make web-dev` | Vite dev server against the development instance |
| `make site` / `make site-dev` | Build or serve the site (docs included) on `:47790` |
| `make release` | Cross-compile linux/darwin × amd64/arm64 plus `checksums.txt` |
| `make clean` | Remove build output |

## Code style

**Go**

- Standard library first. A new third-party module needs a strong reason:
  add a blank import to `internal/deps/deps.go` and run `go mod tidy`, so
  parallel branches don't fight over `go.mod`.
- `gofmt` and `go vet` clean. Doc comments on exported identifiers.
- Wrap errors with context (`fmt.Errorf("open database: %w", err)`). Return
  `*httpx.Err` values (`httpx.NotFound("…")`) from services, so status codes
  stay correct.
- `context.Context` is the first parameter of anything that blocks. Every
  subprocess and outbound call has a timeout.
- No global mutable state outside `main` and wiring. No goroutine without a
  way to stop it.
- Never build shell strings from input. Use `exec.Command` with an argv
  slice.
- Features talk to each other only through `core.Deps` interfaces and the
  event bus. They never import one another.

**Web**

- TypeScript strict, Preact function components, `@preact/signals`.
- Plain CSS with the design tokens from `web/src/styles/tokens.css`. No
  Tailwind, no CSS-in-JS runtime. Both themes (Carbon and Paper) must work.
- Mobile first: 44 px touch targets, and layouts that respect the software
  keyboard and safe areas.
- Accessibility is required: labels, roles, focus order, contrast, reduced
  motion.
- Heavy modules (xterm, CodeMirror, noVNC, Markdown, highlight.js) are
  lazy-loaded. Check the bundle budget in the `pnpm build` output.
- No new runtime dependency without a strong reason.

## Tests

- Go tests live next to the code (`*_test.go`). Make them table-driven and
  keep them off the network. Use `store.OpenMemory()` and `httptest` for
  handlers, and a `core.Deps` with only the fields you need.
- Anything that touches agent configs, `$HOME` or tool installs must run
  against a **temporary HOME** (`t.Setenv("HOME", t.TempDir())`).
- Web: vitest for pure logic (key mapping, parsers, ranking). Use the
  `/dev/ui` kitchen-sink page for visual review, and Playwright scripts in
  `web/e2e/` for end-to-end flows against a dev instance.
- Fixtures are synthetic. Write small files that mimic the real format.
  Never copy real data.

Run one package's tests while you work:

```bash
scripts/dev/safe go test ./internal/files/...
```

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org):
  `feat(terminal): …`, `fix(auth): …`, `docs: …`, `refactor(files): …`,
  `test(agents): …`, `chore: …`. The scope is the feature package or area.
- Keep commits small and focused. Describe user-visible changes in the
  body.
- One topic per pull request. Fill in: what changed, why, how you tested
  it (commands and results, plus screenshots for UI changes in both themes
  and on mobile), and anything left open.
- `make check` passes, and new behaviour has tests.
- Update the docs in the same pull request: `docs/` for users, and
  `docs/dev/` for contributors. Add an entry under **Unreleased** in
  [CHANGELOG.md](CHANGELOG.md).
- Security-sensitive changes (auth, proxies, file paths, subprocesses)
  should say which rules in `docs/dev/SECURITY.md` apply, and how the change
  follows them.

## Recipes

### Add an agent adapter

Adapters live in `internal/agents/`, one file per agent, all implementing
the same interface. [docs/dev/AGENT_ADAPTERS.md](docs/dev/AGENT_ADAPTERS.md)
documents every existing adapter.

1. Copy the adapter closest to your agent (for example one whose history is
   JSONL) to `internal/agents/<id>.go`.
2. Fill in the identity: `id` (lowercase, stable, used in session ids
   `<id>:<nativeId>` and in `agents.disabled`), name, vendor, brand colour,
   and the binary names to look for.
3. **Detection:** the `PATH` plus the agent's usual install folders, and the
   version from `--version` with a 2-second timeout.
4. **Commands:** interactive (with an optional initial prompt and model),
   resume and fork (only if the CLI really supports them; check
   `<cli> --help`), and headless (for Quick AI and schedules). Build argv
   slices. The prompt is always a single argv element.
5. **History reader:** parse the agent's on-disk format into
   `api.AgentSession` and `api.AgentMessage`/parts. Support incremental
   reads (track size, mtime and offset for append-only files), truncate large
   tool output, and derive a title from the first user prompt.
6. **Usage:** extract token counts if the transcripts have them. Add model
   prices to the embedded price table.
7. **Hooks** (optional): if the agent can run a command on "needs input" or
   "turn finished", implement install/remove (with a timestamped backup,
   idempotent) and parse its payload in `relay hook <id> <event>`.
8. Register the adapter, add the agent to `web/src/lib/agents.ts` (name,
   monogram, colour), and add a toolbox recipe if there is an official
   installer.
9. Tests: a synthetic fixture for the reader, command construction, and hook
   install/remove on a temp HOME.
10. Document it in `docs/dev/AGENT_ADAPTERS.md` and in the table in
    [docs/guides/agents.md](docs/guides/agents.md).

### Add a command-center extension

The command center has two halves: client-side **commands** and server-side
**search providers**. The UI-side API is documented in
[docs/dev/UI_KIT.md](docs/dev/UI_KIT.md). The ranking and the provider
contract are in [docs/dev/COMMAND_CENTER.md](docs/dev/COMMAND_CENTER.md).

**A client command** (navigation, an action, a sub-view):

1. Add it to your area's `web/src/routes/<area>/commands.ts`. Keep that file
   light, with no heavy imports:

   ```ts
   import type { Command } from '../../command/registry'

   export const commands: Command[] = [
     {
       id: 'files.trash',                 // "<area>.<verb>"
       title: 'Open Trash',
       section: 'Files',
       area: 'files',
       keywords: ['deleted', 'restore', 'bin'],
       icon: 'trash-2',
       run: (ctx) => ctx.navigate('/files/trash'),
     },
   ]
   ```

2. For inline arguments, set `args: [{ name: 'port', placeholder: 'port' }]`
   and read `ctx.args.port` in `run`.
3. For a Raycast-style sub-view, set `view: { id, title, component: () =>
   import('./MyView') }`. The component receives `{ ctx, query, pop }`.
4. New areas must be added to `web/src/command/all.ts`.

**A server search provider** (results that come from the machine):

1. In your feature package, implement `core.SearchProvider`:

   ```go
   // Scope names the result group, e.g. "snippets".
   func (p *provider) Scope() string { return "snippets" }

   // Search must be fast and bounded: the federated search has a 150 ms budget.
   func (p *provider) Search(ctx context.Context, q string, limit int) []api.SearchResult {
   	// query your own tables with ctx and limit…
   }
   ```

2. Register it during wiring: `a.D.Search.Add(svc.SearchProvider())`.
3. Test it with a table of queries and expected ids.

### Add a toolbox recipe

Recipes are shell scripts embedded in the binary, in
`internal/toolbox/recipes/<id>.sh`. Each one starts with a metadata header.
The exact keys are documented in [docs/dev/TOOLBOX.md](docs/dev/TOOLBOX.md);
copy an existing recipe from the same category as your template. A recipe
declares:

| Field | Meaning |
| --- | --- |
| `id`, `name`, `category`, `description`, `homepage` | Identity, and where it appears |
| `check` | A command that proves the tool is installed |
| `version` | A command that prints the installed version |
| `requires-sudo` | Whether the install needs root |
| `platforms` | For example `linux/amd64 linux/arm64 darwin/arm64` |
| `size` | Approximate download size |

Rules:

1. Use the tool's **official** install method (the distro package manager
   where it is current, otherwise the vendor's installer or release
   tarball). Link to the source in the header.
2. `set -euo pipefail`. Make it idempotent: re-running it upgrades or does
   nothing. Detect the OS and architecture, and fail clearly on unsupported
   ones.
3. Print what you are about to do, since the user watches it in a terminal.
   Ask for `sudo` only for the steps that need it.
4. Never pipe an unverified download into a shell as root. Verify checksums
   when the vendor publishes them.
5. Add a test that parses your header. Do not run real installs in tests.

### Add an API endpoint

1. **Types:** add request and response structs to a new file,
   `internal/api/<feature>.go` (or to your feature's existing file), with
   `json:"camelCase"` tags. Mirror them field for field in
   `web/src/api/<feature>.ts`.
2. **Handler:** in your feature's `Routes(rt *server.Router)`, register it
   at the right level:
   - `rt.Handle` for authenticated JSON (the default),
   - `rt.WS` for WebSockets,
   - `rt.Public` only for endpoints that must work signed out. These need a
     review note, per `docs/dev/SECURITY.md`,
   - `rt.Raw` for proxies and the SPA, where the handler calls
     `rt.Authenticate(r)` itself.
3. Use `httpx.Decode` (it caps the body size), `httpx.OK` and `httpx.Fail`.
   Validate every input. Clean and confine paths to `files.root`. Long work
   returns `202` and publishes progress events.
4. Destructive actions are `POST`/`DELETE` and publish `core.BusAudit`.
5. **Tests:** `httptest` with `store.OpenMemory()`, covering success, bad
   input, not found, and the unauthenticated case.
6. **Docs:** add the endpoint to [docs/dev/API.md](docs/dev/API.md), and to
   [docs/reference/api.md](docs/reference/api.md) if users will call it.

### Add a CLI command

Create `internal/cli/cmd_<name>.go` (or a file in your feature package) and
call `cli.Register` from `init()` with a name, group, summary, usage, flags
and `Run`. Commands that talk to the server use `cli.CallLocal`, which goes
through the control socket. Add the command to
[docs/reference/cli.md](docs/reference/cli.md).

## Contribute to the docs

- **User docs** are in `docs/`. The site renders them with Starlight, and
  GitHub shows the same files. **Contributor docs** are in `docs/dev/`.
- Every user doc starts with frontmatter:

  ```markdown
  ---
  title: Turn on notifications
  description: One sentence that says what the page helps you do.
  ---
  ```

  Don't add an H1: the title is the heading. Start sections at `##`, and
  don't skip levels.
- Link with **relative paths** to `.md` files (`../guides/agents.md#usage-and-quotas`),
  so links work on GitHub and on the site. Link to `docs/dev/` pages with
  full GitHub URLs, because the site does not publish them.
- **Style:** plain words, short sentences, second person ("you").
  Headings name tasks ("Turn on notifications"). Use numbered steps that say
  what you should see. Explain any jargon in one line the first time.
  Warnings go in callouts (`:::caution … :::`). Every guide opens with a
  one-paragraph summary and ends with **Next steps**.
- Examples use `example.com` domains, documentation IP addresses
  (`203.0.113.0/24`, `198.51.100.0/24`) and made-up names. Never use real
  hostnames, IPs, paths or transcript content.
- Screenshots go in `docs/assets/screenshots/`. Take them with
  `node scripts/dev/shot.mjs <url> <out.png> [--mobile]` against a mock or
  dev instance, in both themes where it matters.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
