---
title: Terminal
description: Persistent terminals that work on a phone. The key bar, compose box, gestures, Mac and Windows shortcuts, image paste, recordings and the relay command.
---

Relay's terminal is a real shell on your machine, in your browser. Sessions
run on the machine, under Relay's session daemon, and not in your browser.
You can close the tab, lose signal or restart Relay, and your shell and
anything running in it keep going. When you come back, the screen and the
scrollback are restored. On a phone, you get a key bar, a compose box that
supports autocorrect and dictation, and gestures for the cursor and
scrolling. On a computer, the editing shortcuts you already know from your
Mac or PC work as expected.

## Open and manage terminals

- **New terminal:** select **New terminal** on the Terminal screen, press
  ⌥⌘T (Mac) or Ctrl+Alt+T (Windows/Linux), or choose **Terminal here** from
  a workspace, a folder in Files, or the command center.
- **Switch:** on a computer, sessions are tabs. Use ⌥⌘← / ⌥⌘→ (Mac) or
  Ctrl+Alt+← / → (Windows/Linux), or click a tab. Drag tabs to reorder
  them. On a phone, tap the session name at the top to open the session
  switcher.
- **Rename, pin, close:** use the **⋯** menu on the session. Closing a
  session that is still running asks you to confirm. Pinned sessions stay
  at the front.
- **Split view** (computer): open two sessions side by side from the ⋯ menu,
  and drag the divider to resize them.
- **Status dots:** a green pulse means *working* (output in the last
  moment). A blinking orange dot and the words **Needs you** mean *waiting
  for you*. A hollow ring means *idle*, and a grey dot means *exited*.

### Bring in existing tmux sessions

If the machine already has tmux sessions, they appear in the list marked
**tmux**. Select one to attach to it through Relay. The tmux session keeps
running as before. To hide these entries, set
[`terminal.import_tmux`](../reference/configuration.md#terminal) to `false`.

### After a reboot

Sessions end when the machine restarts, but Relay remembers them. Exited
sessions show a **Restore** button that starts the same command again in
the same folder.

## Use the terminal on a phone

<!-- screenshot: docs/assets/screenshots/terminal-mobile.png: phone terminal with key bar and compose bar -->

The terminal fills the space above your keyboard. Below it are two bars:
the **key bar** and the **compose bar**.

### The key bar

The key bar holds the keys a phone keyboard lacks: **Esc, Tab, Ctrl, Alt,
↑ ↓ ← →, `|`, `~`, `/`, `-`, Home, End, PgUp, PgDn**, plus shortcuts
**^C, ^D, ^Z, ^L, ^R**. Scroll it sideways to see more keys.

- **Ctrl and Alt are sticky.** Tap once for the next key only (the key
  lights up). Double-tap to lock it on, and tap again to release it. For
  example, tap **Ctrl**, then type `r` to search your shell history.
- **Long-press a key for alternatives**, for example `-` → `_`.
- **Customise the bar** in **Settings → Terminal → Key bar**. You can add,
  remove and reorder keys.

### Move the cursor by swiping

Swipe **left or right along the key bar** to move the cursor one character
at a time. Swipe faster to move faster. Swipe **up or down** on the key bar
to step through your command history. It works like a small trackpad for
text.

### Type with the compose bar

The compose bar is a normal text box, so everything your phone keyboard can
do works: autocorrect, swipe typing, dictation and paste. It is the best
way to write long prompts for an agent.

- **Enter** sends the text to the terminal, followed by Return.
- **Shift+Enter** (or the ⤶ key) adds a new line without sending.
- The **microphone** button dictates, where the browser supports speech input. Your keyboard's
  own microphone key also works.
- The **snippets** button inserts a saved prompt. See
  [Command center → Snippets](command-center.md#save-snippets).

**Direct mode** hides the compose bar and sends every key straight to the
terminal. Use it for programs that react to single keys, such as `vim`,
`htop` or `less`. Toggle it from the ⋯ menu. By default, agent sessions open
with the compose bar and plain shells open in direct mode.

### Gestures

| Gesture | What it does |
| --- | --- |
| Tap the terminal | Shows the keyboard (direct mode) or focuses the compose bar |
| Drag up or down | Scrolls back through output, with momentum. In full-screen programs it scrolls the program |
| Long-press | Opens text selection with **Copy**, **Copy all** and **Share** |
| Pinch | Changes the font size (9–22 px). Relay remembers it per device |
| Tap **↓** (appears when scrolled up) | Jumps back to the bottom |

In landscape, the key bar shrinks to one row to leave more room for output.

### Copy text

Long-press the terminal to open a selection view of the visible text. Your
phone's usual selection handles work there. Use **Copy last output** in
the ⋯ menu to copy everything the last command printed.

## Keyboard shortcuts on a computer

Relay translates the editing shortcuts you know into the key sequences that
shells (bash, zsh, fish) and agent programs (Claude Code, Codex, Gemini CLI,
OpenCode and others) understand. Choose a profile in **Settings → Terminal →
Shortcuts**: **Auto** (follows your computer), **Mac**, **Windows/Linux**,
or **Off** (every key is passed through unchanged).

| Action | Mac | Windows / Linux |
| --- | --- | --- |
| Delete to start of line | ⌘⌫ | Ctrl+Shift+⌫ |
| Delete previous word | ⌥⌫ | Ctrl+⌫ |
| Delete to end of line | ⌘⌦ (fn⌘⌫) | Ctrl+Shift+Del |
| Delete next word | ⌥⌦ | Ctrl+Del |
| Start of line | ⌘← | Home |
| End of line | ⌘→ | End |
| Previous word | ⌥← | Ctrl+← |
| Next word | ⌥→ | Ctrl+→ |
| New line in an agent prompt | ⇧⏎ | ⇧⏎ |
| Clear screen and scrollback | ⌘K | Ctrl+Shift+K |
| Copy selection | ⌘C | Ctrl+Shift+C, or Ctrl+C while text is selected |
| Paste (text or images) | ⌘V | Ctrl+Shift+V or Ctrl+V |
| Select all | ⌘A | Ctrl+Shift+A |
| Find in scrollback | ⌘F | Ctrl+Shift+F |
| Bigger / smaller / reset font | ⌘+ / ⌘− / ⌘0 | Ctrl+= / Ctrl+− / Ctrl+0 |
| New terminal | ⌥⌘T | Ctrl+Alt+T |
| Previous / next tab | ⌥⌘← / ⌥⌘→ | Ctrl+Alt+← / → |

**Ctrl+C without a selection always interrupts** the running program, as it
does in any terminal. Relay never overrides browser shortcuts such as ⌘T,
⌘W and ⌘N. If you [install Relay as an app](../getting-started/first-steps.md#add-relay-to-your-home-screen),
fewer shortcuts are taken by the browser.

All shortcuts are listed in [Keyboard shortcuts](../reference/keyboard-shortcuts.md).

## Paste images and files

Agents often need a screenshot. Relay uploads what you paste and types its
path into the terminal:

- **Paste** an image (⌘V / Ctrl+V), or **drag and drop** a file onto the
  terminal.
- On a phone, tap the **attach** button in the compose bar and pick a
  photo or a file.
- On Android, you can also **share** an image from another app to Relay,
  if Relay is installed as an app.

Relay uploads the file in chunks, so a flaky connection resumes instead of
starting over. It saves the file under Relay's upload folder, one folder
per day, and inserts the path at the cursor (quoted if it contains
spaces), for example:

```text
~/.local/share/relay/uploads/2026-10-01/screenshot-1.png
```

The largest upload is set by [`terminal.upload_max_mb`](../reference/configuration.md#terminal)
(512 MB by default).

## Links, paths and the clipboard

- **Web links** in the output are clickable. A `localhost:5173` link opens
  the matching [preview](previews.md), so it works from your phone too.
- **File paths** such as `src/app.ts:42` are clickable and open the file in
  Files at that line.
- **Copy from programs:** when a program copies with OSC 52 (tmux, neovim
  and many CLIs can), the text lands on your device's clipboard and in
  Relay's [clipboard history](command-center.md#use-clipboard-history).
- **Notifications from programs:** a terminal bell or an OSC 9 / OSC 777
  notification marks the session *Needs you* and can notify your phone.

## Search, zoom and themes

- **Find in scrollback** with ⌘F / Ctrl+Shift+F, or **Find** in the ⋯ menu.
- **Font size:** pinch on a phone, or use ⌘+ / ⌘− on a computer.
- **Theme:** the terminal follows Relay's theme (Carbon dark, Paper light,
  or Auto). Font, cursor style and copy-on-select are in **Settings →
  Terminal**.
- **Screen reader mode** is in the ⋯ menu.

## Record and replay sessions

Relay records **agent sessions** by default, so you can watch what an agent
did while you were away.

1. Open the session's ⋯ menu and choose **Recording**.
2. Play the recording, drag the timeline to jump, and change the speed.
3. Select **Download .cast** to keep it. The file is in the standard
   asciicast v2 format, which `asciinema play` and other players can open.

To record every session, or none, set [`terminal.record`](../reference/configuration.md#terminal)
to `all` or `off`. Recordings are deleted after 14 days by default
(`terminal.record_days`).

## Use the `relay` command inside a terminal

Every Relay terminal knows which session it is (`RELAY_SESSION`) and can talk
to Relay through a private local socket (`RELAY_SOCKET`). No password or
token is needed, because only your user can use the socket.

| Command | What it does |
| --- | --- |
| `relay notify "Build finished"` | Sends a notification to your devices |
| `relay notify -t Deploy --kind done "v2 is live"` | …with a title and a kind |
| `some-command \| relay clip` | Copies the output to Relay's clipboard, available on all your devices |
| `relay clip -p` | Prints the latest clipboard entry |
| `relay open src/app.ts:42` | Opens the file at that line on the device you are using |
| `relay preview 5173` | Prints the preview address of port 5173 and a QR code |
| `relay ls` | Lists terminal sessions |
| `relay attach <id or name>` | Attaches to another session in this terminal (detach with Ctrl+\\ then d) |
| `relay run --name tests -- make test` | Starts a command in a new background session |

A useful pattern for long jobs:

```bash
make test && relay notify "Tests passed" || relay notify --kind attention "Tests failed"
```

Every command and flag is in the [CLI reference](../reference/cli.md).

## Next steps

- [Agents](agents.md): the terminal is where agents run. Learn the board,
  history and hooks.
- [Keyboard shortcuts](../reference/keyboard-shortcuts.md)
- [Notifications](notifications.md)
