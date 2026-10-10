---
name: verify-relay
description: Drive relay's real web UI, HTTP API, CLI and terminals in an isolated instance and record a .proof/ bundle. Use before calling a relay change done, to reproduce a relay bug, or to check a feature still works.
---

# Verify relay

Relay is a local web app for persistent terminals, files and coding agents. A finished run names one behaviour and keeps the bundle after the instance is gone.

This skill starts an isolated instance, drives it the way a user does, and records evidence that outlives cleanup. In Codex, invoke it as `$verify-relay`.

`ctl` below means `node .agents/skills/verify-relay/control-relay.mjs`. Every command prints JSON. `ctl --help` lists all commands with examples.

Read `references/features/README.md` first, then the feature file for the change under test.

## Launch

- `ctl up` builds the web UI and `bin/relay-verify`, writes an isolated `relay.toml`, and starts `relay serve`. It picks a free port in 47700-47799, puts all state under an isolated `RELAY_HOME` in `/tmp/rv<8 hex>`, and waits until GET /api/v1/health answers by polling. Running it twice reuses the live run.
- `ctl up --dry-run` prints what it would build and start.
- Never start relay by hand for a proof run. The tool records the processes it owns, and only those get stopped.

## Doctor

Run `ctl doctor` before the first drive, after any failed drive, and whenever something looks off. Do not drive while any check says `fail`.

It checks that RELAY_SOCKET, RELAY_SESSION, RELAY_CONFIG is not inherited (an inherited socket, session or config file points at the user's live instance), that the build is newer than its sources, that built web assets exist under internal/web/dist/assets, so the served UI is the real build, and that every process and the port belong to this run.

## Drive

- HTTP: `ctl api <METHOD> /path [--data json] --out <file>`. Use `--capture name=field` to keep a token and `--bearer name` to send it; both stay out of the output.
- CLI or terminal: `ctl term run -- <command>` runs with the isolated environment. Add `--pty` when the program needs a terminal.
- Browser: `ctl browser shot /path --out <file.png>` and `ctl browser text /path`, when the repo has `playwright-core` and Chrome is installed (`CHROME` overrides the path).
- Signed-in routes go through the control socket. `ctl term run` sets `RELAY_SOCKET`. `ctl api` calls TCP and keeps no cookie, so use it for public routes such as `GET /api/v1/health` and `GET /api/v1/auth/state`. Set `CHROME` to a Chromium binary before `ctl browser`. On macOS the unset default is Google Chrome.app. `ctl restart` stops the owned serve process and starts it again. ptyd stays up. Creating a terminal needs a pty slave. When that open is refused, stop the terminal recipe and report the prerequisite.
- Follow the feature file's recipe: its exact commands, stable handles and observable results. Drive what a user drives. Test-only endpoints, internal setters and mock modes (VITE_MOCK=1) are never proof.

## Evidence

Prove a committed state: commit first, then drive on the clean tree. The bundle records `head_sha` and the uncommitted-diff hash, and any later commit makes it stale. `.proof/` is gitignored, so recording evidence does not dirty the tree.

1. `ctl evidence init --slug <slug> --claim "<one falsifiable sentence>" --feature <id> --level <L2|L3|L4> --required-level <level>`. Required levels: bug fix L4, feature L3, refactor L3, docs or config L1.
2. After each action, save what you observed to a file, then `ctl evidence add --action "<what you did>" --assert "<what you checked>" --artifact <file>` (every step needs an artifact) with `--expect-contains "<text>"`, `--expect-exit <n>`, or `--expected <v> --observed <v>`. Add `--readback` to a step that reads the side effect back through a second path.
3. `ctl down`, then `ctl evidence close`. Close computes the verdict and lowers the level when the steps do not support it. Never edit `manifest.json` or `verdict.md` by hand.
4. L4 (bug fixes): run the same recipe on the base commit first. `git worktree add /tmp/relay-base <base-sha>`, then drive it with `ctl --repo /tmp/relay-base ...` (this checkout's tool works on older commits). It must close NOT VERIFIED. Close the head bundle with `--base-run /tmp/relay-base/.proof/<bundle>` and remove the worktree.

The verdict is VERIFIED, NOT VERIFIED or INCONCLUSIVE. INCONCLUSIVE is never a pass. A feature you cannot reach is reported with its prerequisite (account, OS, entitlement) and the route you tried, never proven through a different entry point.

## Cleanup

- `ctl down` stops only the processes this run started, after proving each is its own: the start time and command recorded by `ps`, or an open file inside the run directory seen by `lsof`. A process it cannot prove stays running and is reported as a leftover. It keeps redacted logs under `.proof/.control/<run>-logs/` and removes the run directory only when it carries this run's marker. `ctl down --dry-run` shows what it would stop.
- Run `ctl down` after every failed attempt as well.
- Never use `pkill -f`, and never stop a relay instance this tool did not start.
- Cleanup never deletes `.proof/`. Check the bundle still exists after `down`.
- The first-run serve log contains a one-time setup code. Do not copy that log into a bundle, a commit or a reply.

## Owned files

This skill owns these files. Its upkeep edits only them:

- `.agents/skills/verify-relay/` (this file, `agents/openai.yaml`, `references/features/`, `control-relay.mjs`)
- `.claude/skills/verify-relay` (a symlink to the folder above) and `.claude/skills/verify/SKILL.md`
- the `verify-relay` block in `AGENTS.md`

Upkeep never edits product code. When the app no longer does what the map says, that is either map drift (fix the map) or a product regression (report it).
