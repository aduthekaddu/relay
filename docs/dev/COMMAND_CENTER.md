# Command center (backend)

Owner: `internal/search` (+ the `snippets`, `notes` and `scripts`
providers). Routes: `API.md` § command center. The palette UI debounces
keystrokes and calls `GET /api/v1/search` for every query.

## Federated search

`GET /api/v1/search?q=&scopes=a,b&limit=8` → `api.SearchResponse`.

1. The query is trimmed, whitespace collapsed, capped at 200 runes.
2. Every registered `core.SearchProvider` (or only those named in `scopes`)
   runs in its own goroutine with a context that expires after **150 ms**.
   Providers that have not answered by then are left out of this response;
   a panicking provider is recovered and logged. Providers are asked for
   `2 × limit` results.
3. Results are normalised: missing `scope` is filled in, entries without an
   id or title are dropped, duplicates (same scope + id) are removed.
4. Ranking — final score = provider score (clamped to 0–1) plus boosts on
   the lower-cased title:

   | Match | Boost |
   | --- | --- |
   | title equals query | +0.60 |
   | title starts with query | +0.35 |
   | a title word starts with query (`run-deploy`) | +0.20 |
   | title contains query | +0.05 |
   | every query word starts a title word (multi-word queries) | +0.10 |
   | recency (`at`) | up to +0.15, halving every 72 h |

   Ties sort by most recent, then title.
5. Each scope keeps at most `limit` (1–50) results; the merged list is
   sorted by score.

The endpoint is rate-limited to 20 requests/s (burst 40) per server.

### Providers

A provider is any type with `Scope() string` and
`Search(ctx, query, limit) []api.SearchResult`, registered with
`Deps.Search.Add` during wiring. Guidelines:

- Return within the budget; check `ctx.Err()` in loops.
- Scores are 0–1 and describe the match quality inside your domain; the
  service adds the title boosts, so do not duplicate them.
- Set `At` for things with a meaningful "last used" time, `Link` to the
  in-app route, and `Meta` for small action hints.
- An empty query means "recent / suggested items".

Providers registered by this branch: `snippets`, `notes` (internal/snippets)
and `scripts` (internal/search). Others (terminals, agents, files,
workspaces, previews, processes) register themselves in their features.

## Script commands

Executables in `~/.config/relay/commands/` — and `<config dir>/commands`
when `RELAY_HOME` or `XDG_CONFIG_HOME` moves the config dir — become
commands. The format is Raycast-compatible, so existing Raycast script
commands work unchanged. `@relay.<key>` is accepted everywhere
`@raycast.<key>` is, and wins when both are present.

Rules: regular executable files only (symlinks followed), no dot-files,
file names limited to `[A-Za-z0-9._+@-]` (the name is the command id),
metadata read from the first 16 KiB, a file without a `title` is ignored.
Comment prefixes `#`, `//`, `--`, `;` and `REM` are recognised.

| Key | Meaning |
| --- | --- |
| `title` | required; shown in the palette |
| `mode` | `inline` / `compact` → output returned inline; `fullOutput` / `terminal` → task terminal; `silent` → only the last line |
| `description` | subtitle |
| `icon` | emoji or short text (paths/URLs are ignored) |
| `argument1`…`argument3` | `{"type":"text","placeholder":"…","optional":true}`; must be contiguous |
| `currentDirectoryPath` | working directory; `~` expands, relative is relative to the script; default is the script's directory |

```bash
#!/bin/bash
# @raycast.schemaVersion 1
# @raycast.title Git Status
# @raycast.mode inline
# @raycast.icon 🌿
# @raycast.argument1 {"type":"text","placeholder":"repo","optional":true}
cd "${1:-$HOME/code/app}" && git status --short --branch
```

```python
#!/usr/bin/env python3
# @relay.title Tail server log
# @relay.mode terminal
# @relay.description Follow the dev server log
import subprocess; subprocess.run(["tail", "-f", "/tmp/dev.log"])
```

Running (`POST /api/v1/scripts/{id}/run` with `{"args": [...]}`):

- Arguments are passed as argv (never through a shell). Missing optional
  arguments are passed as `""`; missing required ones, extra arguments,
  NUL bytes and values over 4 KiB are rejected with 400.
- inline / silent: 30 s timeout, stdout capped at 64 KiB, stdin empty,
  `NO_COLOR=1 TERM=dumb`; the process group is killed on timeout (504).
  A failing script with empty stdout returns its stderr tail instead. At
  most 4 run at once (429 otherwise).
- terminal: a `task` terminal is created through ptyd and its id returned
  immediately; output is live in that terminal.

The directory scan is cached and invalidated by any change to the
directories' modification time (or after 30 s).

## Quick AI

`POST /api/v1/ask` `{"prompt", "agent"?, "cwd"?}` → NDJSON stream of
`api.AskChunk`:

```
{"t":"text","text":"The build fails because "}
{"t":"text","text":"…"}
{"t":"done"}
```

- Agent: the requested one if installed and headless-capable, else the
  first such agent from `Agents.List` (400 for an unknown or unusable
  agent, 503 if none).
- argv comes from `Agents.HeadlessCommand`; the prompt is a single argv
  element. cwd defaults to the home directory and must be an existing
  absolute directory.
- 120 s timeout; the agent's process group is killed on timeout or when
  the client disconnects. Each stdout read becomes one chunk (≤ 4 KiB,
  never splitting a UTF-8 sequence) and is flushed immediately.
- The stream always ends with `done` or `error` (non-zero exit with the
  stderr tail, or the timeout).
- Limits: one run at a time and 20 runs per sliding hour (429 with
  `retryIn`).
