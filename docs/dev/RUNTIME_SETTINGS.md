# Runtime settings contract

The six editable settings are saved in `relay.toml`. GET and PATCH
`/api/v1/settings` return `SettingsState`: the existing six saved fields,
the exact saved `recordingMode`, an `effective` consumer snapshot and an
`apply` timing entry for each field. PATCH accepts only editable fields;
metadata is read-only. This contract does not implement the Settings screen.

## Consumer and timing policy

| Editable field | TOML persistence | Consumer and synchronization/cache | Saved versus effective value and timing | Existing work and validation |
| --- | --- | --- | --- | --- |
| `workspaceRoots` | `agents.workspace_roots` | serve: workspace discovery, Git authorization, pins/current cwd filtering and agent project readers. Shared Runtime snapshot; discovery and reader caches keyed by roots. | Saved paths retain home notation; effective roots are existing canonical directories. Successful API PATCH applies to the next operation. Manual file edits require serve restart. | Removed roots lose new discovery/authorization unless covered by the independent `files.root` grant. Pins, history and terminals remain. Already admitted operations may finish. Max 32 paths; each must be an existing absolute or home-relative directory. Root aliases are pinned to real targets; retargeted aliases, replaced target directories, prefix siblings and ancestor repositories cannot widen an existing grant. |
| `defaultShell` | `terminal.shell` | ptyd: reloads terminal configuration before each creation; explicit session command wins. Login environment remains cached for the daemon lifetime. | Saved empty string means fallback to the session environment's SHELL, then bash/sh. `effective.terminal.defaultShell` reports the daemon's next default shell. Same-source updated daemon: next session. Older daemon: restart required. Different config/home: different-config. Missing/unreadable daemon: unavailable. | Existing sessions keep their process, command and environment. Nonempty API value must name an absolute executable regular file. Login environment changes and other daemon configuration still require an explicit restart. |
| `defaultCwd` | `terminal.default_cwd` | ptyd: same per-creation file snapshot as shell/recording; explicit cwd wins. | Saved value is home-expanded in the API; effective value comes from the daemon's own home expansion and directory check. Same terminal application states as shell. | Existing session cwd stays unchanged. Empty API input becomes home. New default must be an existing absolute or home-relative directory. Invalid later manual file edits reject new creations without killing existing sessions. |
| `recordAgents` | `terminal.record` | ptyd: same per-creation snapshot; each session pins its recording choice. Info asks ptyd instead of reading a mutable serve config. | Saved bool maps off versus agents/all; read-only `recordingMode` preserves the exact mode. False saves off; true preserves all, otherwise saves agents. Effective mode comes from ptyd; same terminal application states as shell. | Existing recordings/sessions keep their original choice. Explicit session record overrides defaults. Agents mode records explicit agent-kind sessions, not ordinary shells. Unsupported manual recording modes reject new creation and report unavailable. |
| `claudeQuota` | `usage.claude_quota` | serve: quota reader checks a Runtime snapshot before fetching and before exposing a result. Existing 10-minute quota cache retained. | Successful PATCH affects the next request. Manual file edits require serve restart. Effective opt-in is separate from saved opt-in; opt-in does not prove credentials/provider availability. | Opt-out suppresses cached/in-flight results. An already admitted request may finish; opt-in can reuse an unexpired cache. Missing credentials or a disabled Claude adapter returns no Claude quota. No provider credentials are written by settings. |
| `idleMinutes` | both `desktop.idle_stop` and `code.idle_stop` | serve: Desktop and Code read Runtime snapshots on the existing 30-second idle loop. User-defined apps retain their own `apps[].idle_stop`. | Saved scalar represents Desktop minutes. Existing unequal Code/Desktop values are exposed separately as effective duration strings. PATCH explicitly sets both; next idle check. Manual file differences require serve restart. | 0 disables idle stopping. Shortening a timeout can stop an already idle owned app at the next check; active proxy traffic or Desktop viewers protect it. Integer 0–10080 only. Disabled/unavailable features are not enabled or launched by saving. |

Other configuration fields remain startup settings. The new response
metadata does not extend the editable field set. Notification settings use
their separate endpoint and contract.

## Transactions and persistence

App wiring creates `core.Deps.Settings` before any feature starts.
`config.Runtime` owns defensive immutable copies of the full known
configuration. Startup `Deps.Cfg` remains unchanged. A writer holds the
shared lock across file read, partial merge, save and publication; readers
use snapshots. Separate partial PATCHes cannot overwrite one another's
accepted fields. Competing writes to the same field use serialized last-write
semantics, without a revision/compare-and-swap promise.

Validation precedes saving. Known TOML fields outside the patch are
preserved. Save uses a unique private temporary file, fsync, close and
atomic rename with mode 0600. A failed validation/read/write/rename never
publishes a new Runtime state or an audit event. The file's previous bytes
remain unchanged on those failures; temporary files are removed.

GET tolerates unknown TOML fields. PATCH rejects any unknown top-level,
nested or app field with 409 and leaves its bytes unchanged, rather than
discarding it during a rewrite. Accepted rewrites drop comments, as the
existing configuration format already documents. Invalid/unreadable files
produce a safe 500 error without copying credentials into responses/logs.
JSON unknown/read-only fields produce 400. JSON null leaves an editable
field unchanged. Empty PATCH still follows the same rewrite policy.

Environment precedence remains defaults, saved file, supported process
environment overrides, then home expansion. Settings persistence reads
without process overrides or expansion, so it never writes an overridden
public URL or the process home into unrelated saved paths. Only patched
runtime fields change; other active environment values stay in effect.
Future starts apply their own environment and expansion home.

This transaction serializes this serve instance's Settings API. Manual
editors, separate serve writers and other configuration CLI commands are
not coordinated by a cross-process file lock. Manual edits are read on GET;
serve does not hot-reload them. The response reports restart-required for
saved workspace/quota/idle values that differ from the serve snapshot.
Owners should serialize external file edits with API saves.

## Independent daemon contract

CLI wiring gives ptyd a config loader; each new session takes one terminal
snapshot before selecting shell, cwd and recording. Atomic file replacement
prevents mixed old/new fields within that snapshot. An old session retains
its PID, explicit saved restore spec, recording decision and input channel.
No PATCH starts, stops or restarts any service.

The owner-only `GET /v1/settings` daemon route reports its next terminal
defaults and whether the source is file or startup. A private digest of
config path plus expansion home lets serve recognize a different source;
this digest is not included in the browser API. Daemon queries are bounded
to one second. Older 404 responses yield restart-required with unknown
effective defaults. A different source never receives a false adoption
promise. An unsupported file/default state yields unavailable.

`SettingsState.effective.terminal` can be null. Even when a daemon snapshot
is available, it describes future defaults rather than every running
session. Existing sessions have their own TerminalSession metadata. Info's
recording flag is derived from the queried daemon mode; unavailable/old
daemon defaults do not establish recording availability.

## Verification

Maintained regressions live beside config, info, workspaces, agents, apps
and ptyd. They cover authenticated Info/settings concurrency, partial writes,
rollback, unknown-field/comment policy, full known-field preservation,
environment/home precedence, discovery/authorization/cache boundaries,
quota opt-out, idle stopping and daemon defaults/overrides.

`scripts/dev/verify-runtime-settings.py` runs separate serve and ptyd
processes with synthetic credentials and repositories in owned temporary
directories. It uses free loopback development ports, verifies new defaults
and old-session input/PID continuity, then removes only its resources.
`--legacy-binary` adds older-daemon and different-source cases.
Fixture quota/idle checks are separate from real daemon integration; they
do not establish real OAuth, Code-server or VNC availability.

Dated implementation evidence and the exact uncommitted patch are recorded
under the private plan package. No production configuration, live service,
dependency, staging, commit or deployment is part of this change.
