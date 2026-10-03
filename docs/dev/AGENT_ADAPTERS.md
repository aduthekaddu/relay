# Agent adapters: implemented contracts and evidence

Source review: **2026-10-03**, Relay base HEAD
`31deacdbc1eff29483427bd82e42cc4767d39972`. Official references below were
consulted on that date. This is a reference for the Go adapter service, not
acceptance of the planned Agents UI, Quick AI, schedules, or authenticated
provider workflows. See [ARCHITECTURE.md](ARCHITECTURE.md),
[FEATURES.md](FEATURES.md), [API.md](API.md), and the
[user guide](../guides/agents.md).

## Evidence and status vocabulary

- **Conditional (C):** Relay has the constructor, command builder, reader or
  installer shown. It requires a compatible executable, configuration or
  local schema; operations may also require vendor authentication.
- **Unsupported (—):** Relay has no implementation for this capability,
  even if the vendor CLI has a similar feature.
- **Unverified (U):** the installed version, schema compatibility or runtime
  result has not been established. A documented vendor feature does not
  verify Relay's invocation on a particular installation.

**Every installed-version and authenticated-runtime cell is U in this
review.** No agent was installed, invoked, signed in, or sent a provider
request for this review. Constructor and synthetic-reader support are
source evidence only. There are no verified minimum CLI versions: flags
and reader schemas are selected statically, without version negotiation.
Exceptions where current documentation does not establish an attempted
contract are called out below. Unsupported cells are not silently filled
from a related adapter or historical worker report.

The fourteen constructors come from `builtinAdapters` in
[adapter.go](../../internal/agents/adapter.go). `Service.List` in
[agents.go](../../internal/agents/agents.go) copies `Adapter.Caps` directly
into the API; these flags are not per-version probes. The JSON contracts
are `AgentInfo`, `AgentCapabilities`, `AgentSession`, `UsageSummary`,
`Quota` and `HookStatus` in [types.go](../../internal/api/types.go), mirrored
in [types.ts](../../web/src/api/types.ts).

## Capability inventory

Each source link identifies the constructor and its reader. **C/U** means
source implementation exists but installed/authenticated behavior remains
unverified. History and usage additionally require the specific schemas
below; missing records are not proof of zero activity.

| Adapter / source symbols | Detection | Launch | Resume | Fork | Headless | History | Usage | Hooks | Quota |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Claude Code — [newClaude, claudeReader](../../internal/agents/adapter_claude.go) | C/U | C/U | C/U | C/U | C/U | C/U | C/U | C/U | C/U, opt-in API |
| Codex — [newCodex, codexReader](../../internal/agents/adapter_codex.go) | C/U | C/U | C/U | C/U | C/U | C/U | C/U | C/U | C/U, transcript |
| Gemini CLI — [newGemini, geminiReader](../../internal/agents/adapter_gemini.go) | C/U | C/U | C/U | — | C/U | C/U | C/U | C/U, timeout discrepancy | — |
| OpenCode — [newOpenCode, opencodeReader](../../internal/agents/adapter_opencode.go) | C/U | C/U | C/U | C/U | C/U | C/U | C/U | C/U | — |
| Kiro CLI — [newKiro, kiroReader](../../internal/agents/adapter_kiro.go) | C/U | C/U, model flag U | C/U, JSONL only | — | C/U, model flag U | C/U | C/U | — | — |
| Cursor Agent — [newCursor, cursorReader](../../internal/agents/adapter_cursor.go) | C/U | C/U | C/U | — | C/U | C/U | — | C/U, stop only | — |
| Grok — [newGrok, grokReader](../../internal/agents/adapter_grok.go) | C/U | C/U | C/U | C/U | C/U | C/U | C/U | — | — |
| pi — [newPi, piReader](../../internal/agents/adapter_pi.go) | C/U | C/U | C/U | C/U | C/U | C/U | C/U | — | — |
| Hermes — [newHermes, hermesReader](../../internal/agents/adapter_hermes.go) | C/U | C/U, root model flag U | C/U | — | C/U, exact invocation U | C/U | C/U | — | — |
| Amp — [newAmp, ampReader](../../internal/agents/adapter_amp.go) | C/U | C/U | C/U | — | C/U | C/U | C/U | — | — |
| GitHub Copilot CLI — [newCopilot, copilotReader](../../internal/agents/adapter_copilot.go) | C/U | C/U | C/U | — | C/U | C/U | C/U, output only | — | — |
| Aider — [newAider, aiderReader](../../internal/agents/adapter_aider.go) | C/U | C/U | — | — | C/U | C/U | — | — | — |
| Qwen Code — [newQwen, qwenReader](../../internal/agents/adapter_gemini.go) | C/U | C/U | C/U | — | C/U | C/U | C/U | — | — |
| Crush — [newCrush, crushReader](../../internal/agents/adapter_crush.go) | C/U | C/U | — | — | C/U | C/U | C/U, session totals | — | — |

All constructors set `Worktrees: true`. This permits Relay's own optional
Git-worktree creation before launch (`Service.launch` in
[launch.go](../../internal/agents/launch.go)); it does not establish a
vendor's native worktree feature or a completed frontend workflow.

## Installation and version detection

`detector.find`, `detect`, `version` and `parseVersion` in
[adapter.go](../../internal/agents/adapter.go) first try each binary name
on `PATH`, then adapter-specific directories, then `commonBinDirs`.
Relative directories are under `Deps.Paths.Home` (falling back to
`os.UserHomeDir`), not the `RELAY_HOME` data directory. Common
locations include `.local/bin`, `bin`, `.npm-global/bin`, `.bun/bin`,
`.cargo/bin`, `go/bin`, `.volta/bin`, `.deno/bin`, `.yarn/bin`,
`.local/share/pnpm`, `/usr/local/bin`, `/opt/homebrew/bin` and `/usr/bin`.
Filesystem fallback checks executable bits. A found path means
`installed=true`; it does not establish package identity or authentication.

Detection is cached for ten minutes. Version detection runs **the found
binary with `--version`**, with a two-second context timeout, no stdin,
`NO_COLOR=1`, `CI=1`, `TERM=dumb` and a 500 ms wait delay. It extracts the
first version-looking numeric token and caches by path/modification time.
An empty version is possible even when installed. There is no help probe,
supported-version range, or authentication probe. Installation hints are
Toolbox identifiers, not proof of installation or an installer execution.

| Adapter | Binary names, in order | Extra search directory under home | Install hint | Installed/version evidence here |
| --- | --- | --- | --- | --- |
| Claude Code | `claude` | `.claude/local` | `claude-code` | U |
| Codex | `codex` | — | `codex` | U |
| Gemini CLI | `gemini` | — | `gemini-cli` | U |
| OpenCode | `opencode` | `.opencode/bin` | `opencode` | U |
| Kiro CLI | `kiro-cli`, `kiro` | — | `kiro-cli` | U; `--version` contract not established by cited command reference |
| Cursor Agent | `cursor-agent`, `agent` | `.cursor/bin` | `cursor-agent` | U; current reference uses `agent` |
| Grok | `grok` | `.grok/bin` | `grok` | U; reference documents `grok version`, not this detector's flag |
| pi | `pi` | `.pi/agent/bin` | `pi` | U |
| Hermes | `hermes` | `.hermes/bin` | `hermes` | U |
| Amp | `amp` | `.amp/bin` | `amp` | U |
| GitHub Copilot CLI | `copilot` | — | `copilot-cli` | U |
| Aider | `aider` | — | `aider` | U |
| Qwen Code | `qwen` | — | `qwen-code` | U |
| Crush | `crush` | — | `crush` | U |

Binary names, extra directories and hints are defined by each constructor
linked in the capability inventory. No claim here pins a machine's actual
version to the mutable vendor documentation.

## Command builders

These are **Relay's argv**, not recommendations to execute them during a
documentation check. `P` is one prompt argument, `M` an optional model,
`ID` a native session ID, and `UUID` a Relay-generated UUID. Brackets mean
an optional nonempty argument; the displayed command name is replaced by
the detected executable path. Source is each constructor above.

| Adapter | New interactive launch | Resume | Fork | Headless builder |
| --- | --- | --- | --- | --- |
| Claude Code | `claude --session-id UUID [--model M] [P]` | `claude --resume ID` | `claude --resume ID --fork-session` | `claude -p --output-format text [--model M] [P]` |
| Codex | `codex [-m M] [P]` | `codex resume ID` | `codex fork ID` | `codex exec --skip-git-repo-check [-m M] [P]` |
| Gemini CLI | `gemini [-m M] [-i P]` | `gemini --resume ID` | Unsupported | `gemini -p P [-m M]` |
| OpenCode | `opencode [--model M] [--prompt P]` | `opencode --session ID` | `opencode --session ID --fork` | `opencode run [--model M] [P]` |
| Kiro CLI | `kiro-cli chat [--model M] [P]` | `kiro-cli chat --resume-id ID` | Unsupported | `kiro-cli chat --no-interactive --wrap never [--model M] [P]` |
| Cursor Agent | `cursor-agent [--model M] [P]` | `cursor-agent --resume ID` | Unsupported | `cursor-agent -p --output-format text [--model M] [P]` |
| Grok | `grok --session-id UUID [-m M] [P]` | `grok --resume ID` | `grok --resume ID --fork-session` | `grok -p P [-m M]` |
| pi | `pi --session-id UUID [--model M] [P]` | `pi --session ID` | `pi --fork ID` | `pi -p [--model M] [P]` |
| Hermes | `hermes [-m M]`; initial prompt unsupported | `hermes --resume ID` | Unsupported | `hermes -z P [-m M]` (exact invocation U) |
| Amp | `amp`; initial prompt and model ignored | `amp threads continue ID` | Unsupported | `amp -x P`; model ignored |
| GitHub Copilot CLI | `copilot [--model M] [-i P]` | `copilot --resume ID` | Unsupported | `copilot -p P [--model M]` |
| Aider | `aider [--model M]`; initial prompt unsupported | Unsupported | Unsupported | `aider --no-auto-commits --message P [--model M]` |
| Qwen Code | `qwen [-m M] [-i P]` | `qwen --resume ID` | Unsupported | `qwen -p P [-m M]` |
| Crush | `crush`; initial prompt and model ignored | Unsupported | Unsupported | `crush run P`; model ignored |

`Service.launch` adds `PresetID` only for Claude, Grok and pi. General
`Service.Command` does not add it. `Service.Command` and `launch` clear
initial prompts for adapters with `Prompt: false` (Hermes, Amp, Aider,
Crush); headless builders still accept prompts. `withPrompt`, `withFlag`
and `safeArg` build argv without a shell and prefix a leading-dash prompt
with a space. Model/native-ID validation is Relay's own restricted regex,
not acceptance of every vendor-supported value.

`Service.resume` in [launch.go](../../internal/agents/launch.go) requires an
indexed session, a builder, a valid native ID and (for resume) the reader's
`Resumable` flag. Missing capabilities return a conflict. An already live
resume returns its existing terminal. Missing working directories fall
back to home. Forks do not preset the new native ID. Resume/fork do not
accept a new prompt/model. `Service.HeadlessCommand` in
[agents.go](../../internal/agents/agents.go) validates and returns argv;
building argv is not proof that any consumer ran it successfully.

## History and accounting inputs

Paths below are relative to Relay's configured home unless marked
workspace-relative. `sources` and `parse` are methods of the named reader
in the capability inventory. Reader schemas are **conditional source
contracts; compatibility with currently installed versions is U** except
for the limited storage descriptions cited in the official references.
Custom vendor home/session-directory overrides are not generally followed.

| Adapter / reader | History sources and limits | Usage/accounting implementation |
| --- | --- | --- |
| Claude Code / `claudeReader` | `.claude/projects/*/*.jsonl` and `.config/claude/projects/*/*.jsonl`; nested `*/subagents/*.jsonl` are usage-only. User/assistant/tool blocks; meta/compact user records and injected user prompts filtered. | Assistant `message.usage`: input, output, cache read/write; synthetic-model usage excluded. Streaming records deduplicated by message/request IDs with maximum counters (`claudeAssistant`, `maxUsage`). No invoice data. |
| Codex / `codexReader` | `.codex/sessions` and `.codex/archived_sessions` rollout JSONL (bounded walk); names from `.codex/session_index.jsonl`. Session metadata, turn context, response items and events; subagent sessions hidden. | `event_msg` / `token_count` needs both last and total usage; cumulative total is the dedupe key. Input subtracts cache read/write; output includes reasoning, also recorded separately. Partial/missing usage is omitted. |
| Gemini CLI / `geminiReader` | `.gemini/tmp/*/chats/session-*.json` or `.jsonl`, reread as whole documents. Cwd from `.project_root` or project hash matching known workspaces. | Per-message `tokens`: input minus cached, output plus thoughts, cache read and separate reasoning. Tool-token field is not accounted. |
| OpenCode / `opencodeReader` | `.local/share/opencode/opencode.db`: `session`, `message`, `part`; sessions with a parent hidden. | Message tokens input/output/reasoning/cache read/write; output adds reasoning. `message.cost` is a native cost input, not verified billing. |
| Kiro CLI / `kiroReader` | `.kiro/sessions/cli/*.jsonl` with sibling `.json` metadata; `Prompt`, `AssistantMessage`, `ToolResults`. Legacy `.local/share/kiro-cli/data.sqlite3` / `conversations_v2` also read but **not resumable**. | JSON sidecar per-turn input/output/cache counters; `kiroApplyMeta` subtracts caches when possible and sums metering values into internal credits without verifying units. Legacy request metadata also yields tokens. Credits are not a USD bill or exposed usage-summary total. |
| Cursor Agent / `cursorReader` | `.cursor/chats/*/*/store.db`: hex metadata at `meta` key `0`, JSON message blobs ordered by rowid; non-JSON blobs skipped. Reader supplies no cwd. | **Unsupported**: no token or cost extraction. |
| Grok / `grokReader` | `.grok/sessions/*/*/chat_history.jsonl` plus `summary.json`, `usage.json`; sidecar stamps cause rereads. | Per-turn input minus caches, output, cache read/write, reasoning; `costUsdTicks / 1e10` becomes native cost. Current official docs establish the sessions directory, not these complete field schemas. |
| pi / `piReader` | `.pi/agent/sessions/*/*.jsonl`; session header, messages, session info, compaction/branch summaries and model changes. Does not traverse `parentId` to select an active branch; all recognized message entries are read. | `message.usage` input/output/cache read/write/reasoning and `usage.cost.total`. A session-tree file is not necessarily one linear active-branch conversation. |
| Hermes / `hermesReader` | `.hermes/state.db`: `sessions`, `messages`, optional `session_model_usage`; parent/hidden sessions hidden. | `hermesUsage` reads per-model/task counters and prefers `actual_cost_usd` over `estimated_cost_usd` as native cost. Those field names are not provider-billing proof. Missing usage table yields no usage; session totals are not a fallback. |
| Amp / `ampReader` | `.local/share/amp/threads/T-*.json`; thread messages; cwd from the first initial environment tree's file URI. | Message `usage` input/output/cache creation/read counters. No premium-credit/account-charge extraction. |
| GitHub Copilot CLI / `copilotReader` | `.copilot/session-state/*/events.jsonl` plus `workspace.yaml` cwd, or older root `.jsonl`; session/model/user/assistant/tool events. | **Partial**: positive assistant `outputTokens` only; input, caches, premium requests and account charges unsupported. A low/zero USD value cannot establish total consumption. |
| Aider / `aiderReader` | Known workspaces' `.aider.chat.history.md`, segmented by dated chat-start headings; synthetic path/time IDs, user/tool/assistant Markdown. Custom history filenames are not discovered; **not resumable**. | **Unsupported**: Markdown reader extracts no tokens/cost. |
| Qwen Code / `qwenReader` | `.qwen/projects/*/chats/*.jsonl`; own record parser (not Gemini's reader), with native session ID/cwd and message parts. | `usageMetadata`: prompt minus cached, output plus thoughts, cache read and reasoning. Similarity to Gemini does not imply hooks/fork support. |
| Crush / `crushReader` | Projects from `.local/share/crush/projects.json` and known workspaces; project `data_dir` or `.crush/crush.db`: `sessions`, `messages`; **not resumable**. | Session prompt/completion totals and session cost, associated with last observed model (`crushReader.parse`). No per-call/cache allocation; internal aggregate call counts are not provider requests. |

`historyReader`, `source`, `usageRec` and shared parsing limits are in
[history.go](../../internal/agents/history.go). The indexer in
[indexer.go](../../internal/agents/indexer.go) can read history independently
of executable detection; it polls every 20 seconds, skips sources over
2 GiB, and chunks append-only JSONL reads at 8 MiB. Unsupported records and
schemas can be skipped or fail parsing. Empty/hidden sessions are omitted
from the ordinary list; history is not a promise to import every message.
SQLite access in [history_sqlite.go](../../internal/agents/history_sqlite.go)
uses read-only handles or private DB/WAL copies. It does not migrate vendor
databases or establish a complete current vendor schema contract.

### Prices are estimates, not account billing

`priceTable.lookup`, `resolve` and `cost` in
[prices.go](../../internal/agents/prices.go) use the embedded
[prices.json](../../internal/agents/prices.json), labelled **2026-09-30**.
That label is the source table's date, not an independent verification of
today's prices. Model names are normalized, then matched exactly, by
longest model prefix, or by a family heuristic. Prices are USD per million
tokens: uncached input, output, cache read and cache write. Reasoning is
already included in output for pricing and is not charged a second time.

Exact/prefix matches use the table even if a transcript carries native
cost. A positive native cost is used for unknown models or family matches;
family matches without native cost use a heuristic estimate. Unknown
models without positive native cost return zero with `estimated=true`.
**`estimated=false` can mean a table match or a native transcript figure;
neither means an audited charge.** Native figures may themselves be
estimates. Subscription plans, credits, discounts, batch/region/context
tiers, missing transcripts and unrecognized usage are not reconciled to
an invoice. Relay does not fetch account bills.

`sessionCosts`, `usageSummary` and `aggregate` in
[usage.go](../../internal/agents/usage.go) aggregate indexed records by
agent/model/local-calendar day and selected range. Internal Kiro credits
are not part of `UsageSummary`. Reader deduplication and `upsertUsage` in
[index.go](../../internal/agents/index.go) avoid some duplicate records;
they do not prove complete provider accounting.

### Quota evidence

Only Claude and Codex have `Quota: true`. All other twelve adapters have
**unsupported quota display**, regardless of vendor features. Evidence is
implemented by `Service.quotas`, `codexQuotaFromFile`, `codexQuota`,
`fetchClaudeQuota` and `parseClaudeUsage` in
[quotas.go](../../internal/agents/quotas.go).

- **Codex:** scans tails (512 KiB) of up to six newest indexed source files
  for payload `rate_limits.primary` / `secondary`, uses event timestamps,
  plan type, used percentage and reset information; cache is one minute.
  `Quota.Source` is `transcript`. After an observed reset expires, the
  implementation sets used percentage to zero **and `Stale=true`**; this
  is not a fresh account reading. Missing rate-limit records omit the
  quota, not an assertion of unlimited capacity. The complete private
  rollout schema and current account accuracy are U.
- **Claude:** opt-in `usage.claude_quota` (false in
  [Config.Defaults](../../internal/config/config.go)). Reads
  `.claude/.credentials.json` / `claudeAiOauth`, requires a nonempty token
  and rejects an explicitly expired token, then requests
  `https://api.anthropic.com/api/oauth/usage` with the
  OAuth beta header. Cached ten minutes; no credential refresh. Recognized
  five-hour/seven-day windows carry utilization/reset timestamps and
  `Quota.Source=api`. On failure, a prior snapshot is stale or absent.
  **This endpoint is described as undocumented in Relay source; no
  suitable official public API contract was established here. Its current
  schema and authenticated behavior are U.** This review made no request
  and read no credentials.

Quota windows describe capacity snapshots, not token-price estimates or
financial billing. Preserve source, update time and stale/absent state
when presenting them; no quota result proves all workflows are authorized.

## Hooks and live-session matching

Only five adapters have Relay hook installers. Installation status means
recognized configuration text/plugin content, not delivery or provider
authentication. The other nine have **unsupported Relay hooks**, even
where vendor documentation describes hooks, extensions or notifications.

| Adapter | Relay configuration / installer | Parsed meaning and limits |
| --- | --- | --- |
| Claude Code | `.claude/settings.json`; `jsonHooks` | `Notification` → attention; `Stop` → done; `parseClaudeLikeHook` ignores `SubagentStop`. Conditional delivery; current installed schema U. |
| Codex | Top-level `notify` in `.codex/config.toml`; `codexHooks` | `parseCodexHook`: `agent-turn-complete` → done; approval/user-input/elicitation variants → attention if received. Official config documents notification argv/JSON, not that every attention variant is emitted. Those emissions U. Existing non-Relay `notify` is refused, not overwritten. |
| Gemini CLI | `.gemini/settings.json`; `jsonHooks` | `Notification` → attention; `AfterAgent` → done, via `parseClaudeLikeHook`. **Timeout discrepancy:** Relay writes `timeout: 5`; official Gemini schema defines milliseconds. Delivery/reliability U; the installer is not evidence of a working five-second hook. |
| OpenCode | `.config/opencode/plugins/relay-attention.js`; `opencodePlugin` | Generated plugin posts `session.idle` → done and `permission.updated` / `permission.asked` → attention. Current plugin docs list `session.idle` / `permission.asked`; older `permission.updated` event contract U. Generated plugin execution U. |
| Cursor Agent | `.cursor/hooks.json`; flat `jsonHooks`, `version: 1` | `stop` → done via `newCursor.ParseHook`. No approval/attention hook installed. Current CLI hook delivery U. |

Source: [hooks.go](../../internal/agents/hooks.go) (`jsonHooks`,
`hookCommand`, `backupFile`),
[hooks_codex.go](../../internal/agents/hooks_codex.go) (`codexHooks`), and
the Claude/Codex/OpenCode/Cursor constructors in the inventory.
JSON/Codex installers back up existing files before changed writes,
preserve unrelated entries and remove recognized Relay commands.
OpenCode owns a marked plugin file; a preexisting non-Relay replacement is
backed up. These are source properties, not permission to run installation
as documentation verification. No hook/configuration was changed here.

`runHook` / `buildHookRequest` in [cmd_hook.go](../../internal/cli/cmd_hook.go) accept stdin or
Codex's final JSON argv, forwards a bounded payload to the Relay agent-hook
endpoint, print nothing on stdout, and return zero on failures (optional
`RELAY_HOOK_DEBUG` diagnostics go to stderr). The one-second
stdin read and two-second request budgets are sequential, not a universal
two-second end-to-end guarantee. `Service.handleHookEvent` in
[launch.go](../../internal/agents/launch.go) interprets the event and kicks
indexing. Done clears terminal attention; terminal and notification effects
depend on pairing. Config presence alone does not establish either.

`pairTerminals` and `liveState.terminalFor` in
[live.go](../../internal/agents/live.go) pair using a preset/resumed native
ID or hook hint first. Fallback uses the same agent and cleaned cwd with a
start time from ten seconds before to two minutes after terminal creation,
choosing closest candidates; competing distances less than five seconds apart stay
unpaired. Hook lookup prefers `RELAY_SESSION`, then a paired native ID,
then exactly one live same-agent/cwd terminal. This is heuristic identity
matching, not authenticated provider-session verification. Missing cwd or
ambiguous simultaneous starts can leave a session unpaired.

## Current official CLI references and version assumptions

The following are vendor or maintainer references consulted on
**2026-10-03**, not installed-version receipts. Each row applies to that
publisher's current documentation; the compatibility range for Relay's
argv and local reader fields remains **U**. A vendor's additional features
do not expand the Relay capability inventory. Unless explicitly noted,
the complete private transcript/accounting schema is not established by
these pages and remains U.

| Adapter | Official references | What the references establish; remaining contract limits |
| --- | --- | --- |
| Claude Code | [CLI reference](https://code.claude.com/docs/en/cli-reference), [hooks](https://code.claude.com/docs/en/hooks) | Prompt/model, session ID, resume/fork, print/text output; Notification/Stop hook configuration and payload. Current local transcript compatibility and live hook delivery U. |
| Codex | [CLI reference](https://developers.openai.com/codex/cli/reference/), [config reference](https://developers.openai.com/codex/config-reference/) | Interactive prompt/model, resume/fork, exec and skip-Git-repo-check; notify command receives JSON. Pages currently redirect to OpenAI's `learn.chatgpt.com` documentation. Extra parsed attention-event emissions and rollout/quota schemas U. |
| Gemini CLI | [configuration / CLI options](https://geminicli.com/docs/reference/configuration/), [hook reference](https://geminicli.com/docs/hooks/reference/) | Interactive/headless prompt, model, resume; Notification/AfterAgent and millisecond timeout. Relay does not implement fork. Timeout discrepancy and complete current transcript schema U. |
| OpenCode | [CLI](https://opencode.ai/docs/cli/), [plugins](https://opencode.ai/docs/plugins/) | TUI model/prompt/session/fork, `run`; global plugin directory and session/permission events. Legacy permission event and local DB schema U. |
| Kiro CLI | [CLI commands](https://kiro.dev/docs/reference/cli-commands/) | `chat`, positional prompt, resume ID, non-interactive mode and wrap option. **Relay's `chat --model` flag and `kiro` alias are U** in this reference; current local sidecar/legacy DB contracts U. No Relay fork/hooks. |
| Cursor Agent | [parameters](https://cursor.com/docs/cli/reference/parameters), [hooks](https://cursor.com/docs/hooks) | `agent`, positional prompt, model, resume, print/text; stop hook schema. Relay also tries `cursor-agent`; that alias and actual CLI hook delivery U here. Local DB/token accounting not established; Relay usage unsupported. |
| Grok | [CLI reference](https://docs.x.ai/build/cli/reference), [headless](https://docs.x.ai/build/cli/headless-scripting) | xAI's Grok CLI: prompt/model, session ID, resume/fork and `-p`; headless storage under `.grok/sessions`. Does not establish complete JSONL/sidecar fields or Relay's `--version` probe. Other programs named `grok` are not this contract. |
| pi | [CLI options](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/cli.md), [sessions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sessions.md) | Maintainer docs: prompt/model/print, session ID, session lookup/fork and JSONL session-tree storage. Relay's linear reader does not implement the CLI's selected-branch traversal. Complete usage fields/current runtime U. |
| Hermes | [CLI interface](https://hermes-agent.nousresearch.com/docs/user-guide/cli) | Default interactive entry and resume; primary headless examples use `hermes chat -q`, with a separate `hermes -w -z` example. **Relay's exact `hermes -z P [-m M]` and root `-m` are U**; do not substitute another command in a claimed Relay contract. Local DB usage schema U. |
| Amp | [threads](https://ampcode.com/docs/threads), [execute mode](https://ampcode.com/docs/cli/execute-mode) | Interactive CLI, `threads continue ID`, `-x`; vendor modes/authentication have their own conditions. Relay ignores launch prompt/model and has no fork/hooks/quota implementation. Private thread fields U. |
| GitHub Copilot CLI | [command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference) | Interactive prompt/model, resume and programmatic prompt. Vendor hook/premium-request features do not imply Relay implements them; output-only accounting is partial and private event-schema compatibility U. |
| Aider | [options](https://aider.chat/docs/config/options.html) | Model, message, no-auto-commits, chat-history filename and restore-history options. Vendor restore-history is different from Relay native-ID resume, which is unsupported; usage/hooks unsupported. Reader's heading segmentation remains a conditional source contract. |
| Qwen Code | [settings / CLI options](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/settings/), [headless](https://qwenlm.github.io/qwen-code-docs/en/users/features/headless/) | Interactive/model/headless prompt and resume ID; project-scoped JSONL chat storage. Complete field compatibility U. Relay does not implement fork/hooks/quota. |
| Crush | [maintainer README](https://github.com/charmbracelet/crush/blob/main/README.md), [release notes](https://github.com/charmbracelet/crush/releases) | Interactive CLI and headless `crush run` mode (release notes). Exact positional-prompt contract and current DB fields U in these references. Relay ignores model/prompt at launch and does not implement resume/fork/hooks/quota. |

The checkout's `AGENTS.md` contains no Context7 lookup process, and no
Context7 lookup tool was available for this review. Direct official pages
were used; unavailable contracts remain marked U. Later installed-version
and authenticated verification belongs to the separate provider matrix,
not this documentation reference.
