# Terminal UX — the headline feature

Goal: the best terminal a browser has ever had on a phone, and one that
feels native on a Mac or Windows keyboard. Agents are the main workload:
the user mostly reads streaming output, answers prompts, types long
instructions, pastes screenshots, and scrolls back.

## Rendering

- xterm.js 6 with the WebGL renderer (fallback: DOM renderer), unicode 11,
  fit, search, web-links, clipboard (OSC 52), image (sixel/iTerm) addons.
- Themes derived from the app tokens (Carbon / Paper) with ANSI palettes
  tuned for contrast; `--bg-sunken` background.
- Font: JetBrains Mono Variable; default 13 px desktop, 12 px phone;
  pinch or ⌘+/⌘− to zoom (persist per device).
- Write output with `term.write(Uint8Array, cb)` and ack bytes to the
  server for flow control; coalesce writes per animation frame.
- On attach: server replays the ring buffer between `replay-begin` and
  `replay-end`; show a subtle "restored" hint; scroll to bottom.
- Reconnect automatically (backoff 0.5 s → 5 s) with an unobtrusive
  "Reconnecting…" pill; input typed while offline is queued (max 4 KB).

## Mobile layout

```
┌───────────────────────────────┐
│ ‹  claude · refactor-auth  ● ⋯│  compact bar: back, session name, status, menu
├───────────────────────────────┤
│                               │
│  terminal (fills the space    │
│  above the keyboard)          │
│                               │
├───────────────────────────────┤
│ esc tab ctrl alt ↑ ↓ ← → | ~ /│  key bar (scrolls horizontally, 44 px keys)
├───────────────────────────────┤
│ [ Message claude…        ] ⏎ 🎤│  compose bar (toggle)
└───────────────────────────────┘
          (software keyboard)
```

- Use `visualViewport` to size the terminal to the area above the
  keyboard; keep the key bar glued to the keyboard; no page scroll; no
  zoom-on-focus (inputs ≥ 16 px).
- **Key bar** (customisable in Settings): Esc, Tab, Ctrl (sticky: tap =
  one-shot, double-tap = lock), Alt (same), arrows, `|`, `~`, `/`, `-`,
  Home, End, PgUp, PgDn, ^C, ^D, ^Z, ^L, ^R. Haptic tick on Android
  (`navigator.vibrate(8)`). Long-press a key for alternates (e.g. `-` → `_`).
- **Swipe the key bar** left/right = cursor ←/→ (one step per 12 px, with
  acceleration); swipe up/down on it = history ↑/↓. A trackpad for text.
- **Compose bar**: a real `<textarea>` so autocorrect, swipe-typing,
  dictation (OS mic key) and paste all work natively. Enter sends the text
  (with bracketed paste if the app enabled it) followed by CR; Shift+Enter
  (or the ⤶ key) inserts a newline in the textarea. Mic button uses Web
  Speech API when available. Snippets button inserts saved prompts.
  "Direct mode" toggle hides it and sends keystrokes straight to the PTY
  (for vim, htop, TUIs). Default: compose visible for agent sessions,
  direct for shells.
- **Direct mode typing** uses a hidden input we control (not xterm's) and
  diffs `beforeinput`/`input` events so Android IME composition and iOS
  autocorrect do not duplicate or drop characters.
- **Scrolling**: one-finger vertical drag scrolls scrollback with momentum
  (normal buffer). In the alternate screen (TUIs), drag sends mouse-wheel
  sequences when the app enabled mouse tracking, otherwise arrow keys.
  A "jump to bottom" button appears when scrolled up.
- **Selection**: long-press opens a native selection overlay — the visible
  buffer rendered as selectable text over the terminal — so the OS
  handles work; Copy / Copy all / Share buttons. Also "Copy last output".
- **Pinch** zooms font size (clamped 9–22 px).
- **Tap** focuses (shows keyboard) in direct mode; in compose mode focuses
  the compose field.
- Landscape: key bar collapses to one row, compose optional.
- Paste images: from the clipboard (paste event), from the photo picker
  (attach button), or share-sheet target (PWA share target) → upload →
  path inserted.

## Keyboard shortcut translation (desktop)

Profiles: **Auto** (detect platform), **Mac**, **Windows/Linux**, **Off**
(everything passes through). Editing shortcuts map to sequences that
readline, zsh, fish, and the agent TUIs (Claude Code, Codex, Gemini,
OpenCode) understand:

| Mac | Win/Linux | Sends | Meaning |
| --- | --- | --- | --- |
| ⌘⌫ | Ctrl+Shift+⌫ | `\x15` (Ctrl+U) | delete to line start |
| ⌥⌫ | Ctrl+⌫ | `\x17` (Ctrl+W) | delete word back |
| ⌘⌦ / fn⌘⌫ | Ctrl+Shift+Del | `\x0b` (Ctrl+K) | delete to line end |
| ⌥⌦ | Ctrl+Del | `\x1bd` | delete word forward |
| ⌘← | Home | `\x01` (Ctrl+A) | line start |
| ⌘→ | End | `\x05` (Ctrl+E) | line end |
| ⌥← | Ctrl+← | `\x1bb` | word back |
| ⌥→ | Ctrl+→ | `\x1bf` | word forward |
| ⌘K | Ctrl+Shift+K | clear scrollback (local) + `\x0c` | clear |
| ⇧⏎ | ⇧⏎ | `\x1b\r` (configurable: `\n`) | newline in agent prompt |
| ⌘C | Ctrl+Shift+C (and Ctrl+C with a selection) | copy selection | copy |
| ⌘V | Ctrl+Shift+V / Ctrl+V | paste (bracketed) incl. images | paste |
| ⌘A | Ctrl+Shift+A | select all (terminal) | select |
| ⌘F | Ctrl+Shift+F | search scrollback | find |
| ⌘+ / ⌘− / ⌘0 | Ctrl+ = / − / 0 | font size | zoom |

Browser-reserved chords (⌘T, ⌘W, ⌘N) are not overridden; Relay uses
⌥⌘T / Ctrl+Alt+T for "new terminal" and the command center for the rest.
Ctrl+C without a selection always sends SIGINT. The mapping lives in one
pure module (`routes/terminal/keymap.ts`) with unit tests for every row.

## Session UI (desktop)

- Tab strip with status dots; drag to reorder; middle-click close; ⌥⌘←/→
  switch; split view (two sessions side by side) with a draggable divider.
- Right panel (toggle): session info (command, cwd, pid, size, clients),
  recording controls, agent transcript link, attention history.

## Accessibility

- Screen-reader mode (xterm `screenReaderMode`) toggle in the menu.
- All controls reachable by keyboard; key bar buttons have labels.
