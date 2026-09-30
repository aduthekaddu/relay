---
title: Agents
description: Launch, watch, resume and search coding-agent sessions. Covers attention hooks, git worktrees, reviewing changes, and usage and quotas.
---

Relay puts every coding agent on your machine in one place. The **board**
shows what is running now and which sessions need you. **History** lists
every past conversation, read from each agent's own files, with full-text
search. You can resume any session with one tap, start new ones in their
own git worktree, review what an agent changed, and see what your agents
cost. Agents run in normal Relay terminals, so everything in the
[Terminal guide](terminal.md) applies to them too.

## Supported agents

Relay detects every agent on this list that is installed. It looks on your
`PATH` and in common install folders, and shows each agent's version. You
can install missing agents from the [Toolbox](toolbox.md).

| Agent | Command | Quick AI and schedules (headless) | "Needs you" detection |
| --- | --- | --- | --- |
| Claude Code | `claude` | `claude -p` | Hooks (Notification + Stop) |
| Codex | `codex` | `codex exec` | Hook (`notify`) |
| Gemini CLI | `gemini` | `gemini -p` | Hooks where your version supports them, otherwise terminal watch |
| OpenCode | `opencode` | `opencode run` | Hooks where supported, otherwise terminal watch |
| Kiro CLI | `kiro-cli` | `kiro-cli chat --no-interactive` | Hooks where supported, otherwise terminal watch |
| Cursor Agent | `cursor-agent` | `cursor-agent -p` | Hooks where supported, otherwise terminal watch |
| Grok | `grok` | where supported | Terminal watch |
| pi | `pi` | where supported | Terminal watch |
| Hermes | `hermes` | where supported | Terminal watch |
| Amp | `amp` | `amp -x` | Terminal watch |
| GitHub Copilot CLI | `copilot` | `copilot -p` | Terminal watch |
| Aider | `aider` | `aider --message` | Terminal watch |
| Qwen Code | `qwen` | `qwen -p` | Terminal watch |
| Crush | `crush` | `crush run` | Terminal watch |

Resume and fork are offered for each agent whose CLI supports them. The
**Agents → Installed** list shows exactly what each installed agent can do
on your machine. Adapter details for developers are in
[docs/dev/AGENT_ADAPTERS.md](https://github.com/aduthekaddu/relay/blob/main/docs/dev/AGENT_ADAPTERS.md).

To hide an agent you never use, add its id to
[`agents.disabled`](../reference/configuration.md#agents).

## Start an agent session

1. Open **Agents** and select **New session**. You can also choose **Start
   agent here** from a workspace or a folder in Files, or type `new agent`
   in the command center (⌘K / Ctrl+K).
2. **Agent:** pick one of your installed agents.
3. **Workspace:** pick a recent or pinned project, or browse to any folder.
4. **Prompt** (optional): the first instruction. Long prompts are fine.
   You can dictate them on a phone.
5. **Model** (optional): for agents that let you choose.
6. **New git worktree** (optional, git projects only): type a branch name,
   and the agent works in a separate copy of the repository. See
   [Run agents in parallel with worktrees](#run-agents-in-parallel-with-worktrees).
7. Select **Start**. The agent opens in a new terminal named after the agent
   and the task, for example `claude · refactor auth`.

## Watch the board

<!-- screenshot: docs/assets/screenshots/agents-board.png: live board with working, needs-you and idle sessions -->

The board lists every live agent session with its terminal:

| Status | Meaning |
| --- | --- |
| **Working** (green pulse) | The agent is producing output |
| **Needs you** (blinking orange dot) | The agent asked a question, wants approval, or finished its turn and is waiting |
| **Idle** (hollow ring) | Nothing has happened for a while, and nothing is waiting for you |

Each card shows the task title, the folder, the git branch and how long the
session has been running. Tap a card to open its terminal. Sessions that need
you also appear at the top of **Home**, oldest first.

When you type in a *Needs you* session, or select **Mark as seen**, it goes
back to normal.

## Get told when an agent needs you

Relay has two ways to notice that an agent is waiting:

1. **Hooks (most reliable).** Many agents can run a command when they need
   input or finish a turn. Relay installs a hook that runs `relay hook`,
   which tells Relay exactly what happened.
2. **Terminal watch (fallback, works with every agent).** Relay notices a
   terminal bell or notification sequence, or output that stops at a
   prompt the agent is known to show. By default, a session counts as
   quiet after 8 seconds without output
   ([`agents.idle_seconds`](../reference/configuration.md#agents)).

Either way, the session turns *Needs you*, and Relay sends an **attention**
notification to your devices. If you are already looking at that terminal,
there is no push.

### Install the hooks

1. Go to **Settings → Agents**.
2. Next to an agent that supports hooks, select **Install hooks**.
3. Relay **backs up** the agent's config file (with a timestamp in the
   name), then adds its hook. Existing settings are kept.

What Relay adds:

| Agent | File | Change |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` | `Notification` and `Stop` hooks that run `relay hook claude notification` / `relay hook claude stop` |
| Codex | `~/.codex/config.toml` | `notify = ["relay", "hook", "codex", "notify"]` |
| Others | Their own hook configuration, where it exists | A hook that runs `relay hook <agent> <event>` |

**Remove hooks** undoes the change. The hook is safe everywhere: in sessions
started outside Relay it does nothing, and it always exits within about
two seconds, so an agent is never held up by it.

:::note
The hook config files are the only agent files Relay ever writes, and only
after you select **Install hooks**. Relay reads transcripts but never
modifies them.
:::

## Browse and search history

**Agents → History** lists every past session that Relay finds on disk. It
reads them where each agent keeps them, for example `~/.claude/projects`,
`~/.codex/sessions`, `~/.gemini/tmp`, `~/.local/share/opencode`,
`~/.kiro/sessions`, `~/.grok`, `~/.pi` and `~/.hermes`.

- **Group** by day or by project. **Filter** by agent or workspace.
- **Pin** important sessions, **archive** ones you are done with, and
  **rename** a session to give it a better title. These changes are stored
  in Relay; the agent's files are untouched.
- **Search** with the search field, or type in the command center. Search
  covers everything you and the agents wrote, and highlights the matching
  words. Selecting a result opens the transcript at that message.

The first time Relay starts, it indexes your history in the background at
low priority. With a lot of history this can take a few minutes. New
messages are indexed as they are written. To rebuild the index, select
**Reindex** in **Settings → Agents**. To turn off full-text indexing, set
[`agents.index_history`](../reference/configuration.md#agents) to `false`.

## Read a transcript

Open any session to read it as a conversation:

- Messages are rendered as Markdown, with code highlighting.
- **Tool calls and results** (commands the agent ran, files it read) are
  collapsed. Tap to expand them.
- **Diffs** and **images** are shown inline.
- **Thinking** is hidden by default. Use **Show thinking** to reveal it.
- **Copy** any message. **Resume from here** continues the conversation.

## Resume or fork a session

- **Resume** continues the conversation where it stopped, in a new terminal.
- **Fork** starts a new conversation that begins with the same history,
  leaving the original as it was. Fork is offered only for agents that
  support it.

Both are one tap on a session in History, on Home under **Continue**, or
in the command center (type `resume` and part of the title).

## Run agents in parallel with worktrees

A **git worktree** is a second working folder for the same repository, on
its own branch. Two agents in two worktrees cannot overwrite each other's
changes, and your own checkout stays clean.

When you tick **New git worktree** and enter a branch name such as
`fix-login`, Relay runs the equivalent of:

```bash
git worktree add -b fix-login <repo>/.worktrees/fix-login
```

and starts the agent in that folder. Manage worktrees on the workspace's
**Review** screen, where you can list, create and remove them. Add
`.worktrees/` to your `.gitignore` so the folders don't show up as
untracked files.

## Review what an agent changed

For sessions in a git repository, select **Review changes** (on the session,
or from the workspace):

1. The screen shows the branch, how many commits it is ahead or behind, and
   every changed file with lines added and removed.
2. Tap a file to see its diff. On a phone, the file list and the diff are
   separate screens.
3. **Stage** or **unstage** files, or **discard** changes. Discard asks
   for confirmation, and it cannot be undone.
4. Write a message and select **Commit**.
5. **Push** and **Pull** run in a terminal you can watch, so you can answer
   any password or host-key prompts.
6. If the GitHub CLI (`gh`) is installed and signed in, **Open pull request**
   creates a PR for the branch.

## Usage and quotas

**Agents → Usage** shows what your agents cost. Figures are available by day,
by agent and by model, for today, 7 days, 30 days or all time.

- **Cost** is calculated from the token counts in each transcript and a
  built-in price table, including cache reads and writes. It is an
  **estimate** of what the same usage would cost at API prices. If you pay
  a flat subscription, your actual bill differs. Models missing from the
  price table are marked *estimated*.
- **Quotas** show plan limits where Relay can read them:
  - **Codex**: from the rate-limit information Codex writes to its own
    session files. No setting is needed.
  - **Claude**: opt-in. Turn on **Settings → Agents → Read Claude plan usage**
    ([`usage.claude_quota`](../reference/configuration.md#usage)). Relay
    then uses the sign-in token that Claude Code stored on this machine to
    ask Anthropic for your plan usage, at most every 10 minutes. The token
    never leaves your machine except in that request to Anthropic, and it
    is never logged.

## Next steps

- [Notifications](notifications.md): make sure *Needs you* reaches your
  phone.
- [Schedules](schedules.md): give your agents a night shift.
- [Toolbox](toolbox.md): install agents and connect MCP servers.
