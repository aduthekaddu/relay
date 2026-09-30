---
title: Command center
description: One shortcut for your whole machine. Search everything and run actions, script commands, clipboard history, snippets, notes, the calculator and Quick AI.
---

The command center is a search box for your whole machine. Press **⌘K** on
a Mac, **Ctrl+K** on Windows and Linux, or tap the search field in the
header on a phone. Then type. You can find and act on terminals, agent
sessions and conversations, workspaces, files, previews, processes,
snippets, notes and settings. The command center also runs your own scripts,
keeps a clipboard history that follows you across devices, and answers
questions with your installed agents.

## Open it and find things

| Keys | What they do |
| --- | --- |
| ⌘K / Ctrl+K | Open or close the command center |
| Type | Search everything. Results are grouped by kind |
| ↑ ↓ | Move through the results |
| Enter | Run the main action (open, resume, go to…) |
| ⌘Enter / Ctrl+Enter | Run the secondary action |
| ⌘K or → on a result | Open the **actions panel** for that result |
| Esc, or ⌫ in an empty field | Go back one step, or close |

Examples of what you can type:

| You type | You get |
| --- | --- |
| `resume auth` | Agent sessions about "auth". Enter resumes one |
| `kill :3000` | The process listening on port 3000, with **Kill** as the action |
| `open preview 5173` | The dev server on port 5173 |
| `app.ts` | Files with that name in your workspaces |
| `new agent` | The launchpad, with inline fields for agent, workspace and prompt |
| `dark` | The theme switcher |

On a phone, the command center opens full screen with large targets. Tap →
on a result to see its actions.

### Actions

Every result has an **actions panel**. Open it with ⌘K / Ctrl+K on the
result, or → on touch. Examples: a terminal offers open, rename and kill. An
agent session offers resume, fork and copy the ID. A file offers open, copy
path, open in Code and open a terminal here. Actions that destroy something
ask you to confirm.

### Arguments

Some commands take arguments inline. After you choose **New agent session**,
fields appear right in the search box: `[agent] [workspace] [prompt]`.
Press Tab to move between them and Enter to run the command.

### Go-to shortcuts

Outside a text field, press **G** followed by a letter to jump to an area,
for example **G T** for Terminal or **G A** for Agents. Press **?** to see
every shortcut. The full list is in [Keyboard shortcuts](../reference/keyboard-shortcuts.md).

## Built-in extensions

### Calculate and convert

Type a sum or a conversion, and the answer appears as the first result.
Enter copies it.

```text
1920 * 1080 / 3          → 691200
18% of 2400              → 432
2.5 hours in minutes     → 150 min
72 F in C                → 22.2 °C
```

### Use clipboard history

Relay keeps your last 200 clipboard entries (each up to 256 KB) and shares
them between all your devices. Copy on your laptop, then paste on your
phone. Entries are added when you:

- copy in a Relay terminal (including programs that copy with OSC 52),
- pipe something into `relay clip` in any Relay terminal,
- copy on the [remote desktop](desktop.md),
- paste into Relay's own clipboard view.

Type `clipboard` (or `clip`) in the command center to browse the history.
Enter copies an entry to the device you are using, and the actions panel
can paste it straight into the current terminal or delete it. **Clear
clipboard history** removes everything.

### Save snippets

Snippets are saved prompts and commands. Create one by typing `new snippet`,
or in **Settings → Snippets**. Give it a name and a body, and mark it as a
*prompt* (for agents) or a *command* (for shells).

Use `{{variables}}` for the parts that change:

```text
Review the changes on branch {{branch}} and list anything risky in {{area}}.
Do not modify files.
```

When you use this snippet, Relay asks for `branch` and `area`, then inserts
the finished text into the current terminal or the compose bar. Snippets
you use often move up in the results. The compose bar on a phone has a
snippets button too.

### Write notes

A scratchpad for things you want to keep: a command you will need again, a
plan, a phone number for the data centre. Type `new note` to create one, or
type any word to search your notes. Notes are stored in Relay's database on
your machine.

### Ask Quick AI

Type a question and choose **Ask AI** (or start with `ask`). For example:

```text
ask why does `git push` say "non-fast-forward"?
```

Quick AI runs one of **your installed agent CLIs** in headless mode (such
as `claude -p` or `codex exec`), using your own subscription or API key.
The answer streams into the command center. You can choose the agent in
the actions panel. By default, Relay uses the first installed agent that
supports headless mode.

- It runs in your home folder by default. From a workspace, it runs in
  that workspace, so the agent can read your code.
- A single question can run for at most 2 minutes. To keep costs and load
  predictable, only one question runs at a time, and you can ask at most 20
  per hour.
- Nothing goes to Relay's authors. The request goes wherever that agent
  normally sends it.

### System actions

Switch the theme, lock (sign out of this device), sign out everywhere, reload
the app, restart a service, or open any settings page. Type what you want,
such as `theme`, `sign out` or `passkeys`.

## Add your own script commands

Any executable file in `~/.config/relay/commands/` becomes a command in the
command center. The format is compatible with
[Raycast script commands](https://github.com/raycast/script-commands):
metadata goes in comments at the top of the file, and Relay reads either
`@raycast.` or `@relay.` keys.

### A complete example

Save this as `~/.config/relay/commands/biggest-folders.sh`:

```bash
#!/usr/bin/env bash
# Required
# @relay.title Biggest folders
# @relay.mode inline
#
# Optional
# @relay.description Show the ten biggest folders under a path
# @relay.icon chart
# @relay.argument1 {"type":"text","placeholder":"path (default ~)","optional":true}
# @relay.currentDirectoryPath ~

set -euo pipefail
target="${1:-$HOME}"
du -xh --max-depth=1 "$target" 2>/dev/null | sort -rh | head -n 10
```

Make it executable:

```bash
chmod +x ~/.config/relay/commands/biggest-folders.sh
```

Open the command center and type `biggest`. Select **Biggest folders**, type
a path in the argument field (or leave it empty), and press Enter. The
output appears right in the command center.

### Metadata keys

| Key | Required | Meaning |
| --- | --- | --- |
| `title` | Yes | The name shown in the command center |
| `mode` | Yes | How it runs. See the next table |
| `description` | No | A line shown under the title |
| `icon` | No | An icon name, or a short text/emoji fallback |
| `argument1` … `argument3` | No | JSON: `{"type":"text","placeholder":"…","optional":true}`. Values are passed as `$1` … `$3` |
| `currentDirectoryPath` | No | The folder the script runs in (`~` is allowed) |
| `schemaVersion` | No | Accepted for Raycast compatibility, and ignored |

| Mode | Behaviour |
| --- | --- |
| `inline` (Raycast `fullOutput`, `compact`, `inline`) | Runs in the background and shows the output (up to 64 KB) in the command center. Stops after 30 seconds |
| `terminal` | Opens a new Relay terminal and runs the script there. Use this for long or interactive scripts |
| `silent` (Raycast `silent`) | Runs in the background and shows only a short "Done" or the error |

Arguments are passed to the script as separate values, never through a
shell, so spaces and quotes in what you type are safe. Scripts run as your
user, with your normal environment.

:::tip
Raycast scripts you already have usually work as they are: copy them into
`~/.config/relay/commands/` and make them executable. Scripts that rely on
macOS-only tools (such as `osascript`) only work if Relay runs on a Mac.
:::

## Next steps

- [Keyboard shortcuts](../reference/keyboard-shortcuts.md)
- [Terminal](terminal.md): snippets in the compose bar, and `relay clip`.
- [Agents](agents.md): the headless agents that power Quick AI.
