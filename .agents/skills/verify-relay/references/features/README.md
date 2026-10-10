# relay feature map

This folder is the maintained source for verifying what a relay user can do. Read this file before driving, then the feature file that matches the change under test.

## Baseline preconditions

- `ctl` means `node .agents/skills/verify-relay/control-relay.mjs`.
- Start with `ctl up` and require `ctl doctor` to pass. Never drive an instance this run did not start.
- `RELAY_HOME` points at the run's isolated directory under `/tmp/rv<8 hex>`, never the user's real data.
- No account exists until a recipe creates one. The files root and the agent workspace root are the physical path of `$RELAY_HOME/scratch`. Agent hooks and history indexing are off. Desktop, code and previews are off.

## Driving conventions

- Start every recipe from the baseline state unless its preconditions say otherwise.
- Prefer stable handles: `nav[aria-label="Areas"]`, `a[href="/terminal"]`, `h1#login-title`, and paths under `/api/v1/`. Use coordinates or tab order only as a last resort.
- Wait on an observable end state (a status, a response, a line of output), never on a fixed sleep.
- Treat every command as literal. Keep quoted names and flags unchanged.
- Put back any shared fixture a recipe changes. Never remove proof artifacts.

## Proof and skip reporting

- Capture the user action and the resulting state, not only the final screen.
- A mutation needs a read-back through a second path (reload, a GET after a POST, a CLI listing after a UI action), recorded with `--readback`.
- UI proof includes a screenshot or text snapshot with the app's identity visible. CLI proof includes the command, output and exit code.
- Record the feature id and entry point with every bundle.
- Report an unreachable entry point with the command you tried and the unmet prerequisite. Never report it as verified through a different path.

## Feature entry contract

Each feature file starts with an H1 and one paragraph on the user-visible behaviour, then exactly these four H2s in order:

1. `Sub-features`: short ids, one line each.
2. `How to get to it (user POV)`: every entry point a user has.
3. `Driving it with control-relay`: starts with `Preconditions:`, then labelled bullets that pair a user action with an exact `ctl` command and its observable result.
4. `Gotchas`: traps that waste or invalidate a run.

Keep implementation details out. Name only user paths, stable handles, required state, commands and observable proof.

## Features

- `terminal.md`. A shell session stays available after the owned serve process restarts.
- `sign-in.md`. First run shows account creation, and setup clears the setup-required state.
- `files.md`. Saving text writes those bytes, and a later read returns them.
- `agents.md`. The agent list names an installed binary that exists on disk.

## Not yet mapped

- Home (`/`)
- Review and git
- Previews (`/previews`)
- Code (`/code`)
- Desktop (`/desktop`)
- Command center
- Notifications
- Schedules
- System (`/system`)
- Toolbox
