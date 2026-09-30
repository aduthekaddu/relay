# Toolbox and MCP

The toolbox installs coding agents and developer tools from recipes that ship
inside the binary, and wires useful MCP servers into every installed agent.
Code: `internal/toolbox`. Wiring: `internal/app/wire_toolbox.go`.

## Endpoints

| Method | Path | Result |
| --- | --- | --- |
| `GET` | `/api/v1/toolbox[?refresh=1]` | `[]Tool` — every recipe with `installed`, `version`, `installable` for this OS |
| `POST` | `/api/v1/toolbox/{id}/install` | `202` + `TerminalSession` (kind `toolbox`) running the install |
| `GET` | `/api/v1/toolbox/mcp` | `[]MCPServer` — registry × agents matrix (`agents[id] = configured`) |
| `POST` | `/api/v1/toolbox/mcp/apply` | body `MCPApplyRequest {server, agents, remove?}` → updated `MCPServer` row |

Errors: unknown tool/server → `404`/`400`; a recipe not for this platform →
`400`; a tool already installing → `409` (message names the terminal); ptyd
down → `503`; an agent that is not installed → `400`.

### Listing

Each recipe declares a `check` binary and a `version` command. The list runs
all checks concurrently (4 at a time, 5 s per version command, output
capped at 64 KiB) against `PATH` plus the usual user and system bin dirs
(`~/.local/bin`, Relay's own `~/.local/share/relay/tools/node/bin`,
`~/.cargo/bin`, `~/go/bin`, `~/.bun/bin`, `~/.opencode/bin`,
`~/.npm-global/bin`, `~/.deno/bin`, `/snap/bin`, `/opt/homebrew/bin`,
`/usr/local/go/bin`, …). Results are cached for 60 s; concurrent requests share one
refresh; `?refresh=1` forces one, and every finished install invalidates the
cache.

### Installing

An install is always visible. The server prepends `recipes/_lib.sh` to the
recipe, writes it to a `0700` file under `<cache>/toolbox/`, and asks ptyd to
create a terminal of kind `toolbox` that runs it with `bash`. The user watches
every command, types their sudo password there if the recipe needs one, and
the install survives the browser closing. The script deletes itself when it
finishes; leftovers from a crash are removed on the next start.

Progress is published on the event bus as `toolbox.job` with
`ToolboxJob {tool, terminalId, state: running|done|failed, exitCode?}`
(`internal/api/toolbox.go`). The watcher polls the session every 2 s and stops
with the service.

## Recipes

`internal/toolbox/recipes/<id>.sh`, embedded with `go:embed`. Files starting
with `_` are not recipes. Header (`# key: value` lines before the first
non-comment line):

```bash
#!/usr/bin/env bash
# id: gh                               # must match the file name
# name: GitHub CLI
# category: cli                        # agents|runtimes|browsers|desktop|creative|cli|editors
# description: GitHub on the command line — PRs, issues, releases.
# homepage: https://cli.github.com
# check: gh                            # binary whose presence means "installed"
# version: gh --version                # first version-looking token is shown
# requires-sudo: linux                 # yes | no | linux | darwin
# platforms: linux, darwin
# size: 40 MB                          # approximate download, for the UI
# tags: git, github
```

The catalog rejects a recipe with a missing required key, an unknown
category, an id that does not match its file name, or a duplicate id —
`TestDefaultCatalog` enforces this for everything shipped.

`_lib.sh` gives recipes a small vocabulary: `say`/`ok`/`warn`/`die`,
`have`, `need`, `run`, `pm` (apt, dnf, pacman, zypper, apk, brew),
`pkg apt="…" dnf="…" brew="…"` (names per package manager), `as_root` (sudo
only when not already root; the prompt appears in the terminal), `fetch`,
`download`, `sha256_of`, `gh_latest` (latest GitHub release tag),
`pipe_sh` (download a vendor's install script to a file, then run it),
`npm_global` (falls back to a `~/.local` prefix, never `sudo npm`),
`link_bin` and `sudo_write`.
Prefer the distribution package when it is current enough, otherwise the
vendor's official installer or release tarball; nothing is version-pinned.

Every side effect goes through these helpers, so `RELAY_DRY_RUN=1` prints the
commands instead of running them. `RELAY_OS`, `RELAY_ARCH` and `RELAY_PM`
override detection; `TestRecipesDryRun` runs every recipe in dry-run mode on
linux/amd64+apt, linux/arm64+apt, linux/amd64+dnf and darwin/arm64+brew
against a temp `HOME`.

### Adding a recipe

1. Create `recipes/<id>.sh` with the header and a body using `_lib.sh`.
2. `RELAY_DRY_RUN=1 RELAY_PM=apt bash -c 'cat internal/toolbox/recipes/_lib.sh internal/toolbox/recipes/<id>.sh | bash'`
   to read what it would do.
3. `scripts/dev/safe go test ./internal/toolbox/...`.

## MCP

### Registry

| id | command |
| --- | --- |
| `playwright` | `npx -y @playwright/mcp@latest` |
| `chrome-devtools` | `npx -y chrome-devtools-mcp@latest` |
| `blender` | `uvx blender-mcp` |
| `context7` | `npx -y @upstash/context7-mcp@latest` |
| `filesystem` | `npx -y @modelcontextprotocol/server-filesystem <home>` |

### Agents and their config

| agent | file (under `$HOME`) | shape | apply via |
| --- | --- | --- | --- |
| `claude` | `.claude.json` | `mcpServers.<name>` `{type: stdio, command, args, env}` | `claude mcp add -s user …`, else JSON merge |
| `codex` | `.codex/config.toml` | `[mcp_servers.<name>]` `command`, `args`, `env` | `codex mcp add …`, else TOML merge |
| `gemini` | `.gemini/settings.json` | `mcpServers.<name>` | JSON merge |
| `opencode` | `.config/opencode/opencode.json[c]` | `mcp.<name>` `{type: local, command: [..], enabled, environment}` | JSON merge |
| `kiro` | `.kiro/settings/mcp.json` | `mcpServers.<name>` | JSON merge |
| `cursor` | `.cursor/mcp.json` | `mcpServers.<name>` | JSON merge |

An agent is "detected" when its binary is found or its config directory
exists. A server counts as configured for an agent when an entry has the
registry id as its name or its command line contains the server's package
(so hand-made entries under another name are recognised, and removed too).

Merges are conservative:

- the file is backed up first to `<file>.relay-backup-<UTC timestamp>` with
  the original mode;
- JSON is edited with an order-preserving tree (`ojson.go`), so unrelated keys
  keep their order and values; JSONC comments (OpenCode) are understood when
  reading, but a file that contains comments is never rewritten — the apply
  fails with "add the server by hand" rather than lose them;
- Codex TOML is edited textually — only the `[mcp_servers.<name>]` table (and
  its sub-tables) is added or removed, everything else is left byte-for-byte;
- the result is written to a temp file in the same directory and renamed over,
  preserving the file mode (new files are `0600`).

When an agent's own CLI is present and supports it, Relay uses the CLI instead
of editing the file (`claude mcp add -s user [-e K=V] <id> -- <command…>`,
`codex mcp add <id> [--env K=V] -- <command…>`; 60 s timeout; a failing CLI
becomes `502 agent_cli_failed` with the first 400 bytes of its output).
Apply calls are serialised.

## Safety

- Recipes run as the Relay user in a visible terminal; sudo is never used
  silently and never cached by Relay.
- Scripts are created with `os.CreateTemp` (exclusive create), mode `0700`,
  in a `0700` directory, and passed to bash as an argument — no string is
  interpolated into a shell command.
- Downloads use HTTPS; vendor installer scripts are saved to a file and run,
  never piped from the network into a root shell.
- MCP config edits never touch anything but the one server entry, and always
  leave a backup.
