# Relay features & acceptance criteria

The v1 scope. Each bullet is a user-visible behaviour a reviewer can check.
"Old" marks capabilities carried over (reimplemented) from the previous
private gateway; everything else is new.

## 1. Sign-in & security (auth)

- First run: if no password is set, `/login` shows "Create your account"
  (username + password + strength meter). `relay setup`/`relay passwd` can
  also set it from the terminal. (old: shared password login)
- Password sign-in with "Keep me signed in" (30 d) vs session (12 h).
- Passkeys: register from Settings → Security; sign in with one tap
  (discoverable credentials, "Sign in with a passkey" button, conditional UI
  autofill when supported).
- Optional TOTP second factor (QR shown, 6-digit codes, ±1 step skew).
- Rate limiting: 5 attempts / 5 min per IP, exponential backoff, global cap;
  generic error text; failed attempts audited. (old: 0.4 s delay)
- Devices: list of signed-in sessions (device, browser, OS, IP, last seen),
  revoke one / all others; changing the password revokes others.
- API tokens for automation (shown once, hashed at rest, last-used time).
- Audit log (sign-ins, failures, token use, revocations, destructive file
  and process actions). (old: activity page)
- New device sign-in → "security" notification to other devices.

## 2. Home

- "Needs you" strip: every session waiting on the user (agent name, task,
  why, how long), one tap to open. Empty → quiet "All clear".
- Running now: live terminals and agents with status, last line preview,
  duration, workspace.
- Workspaces: recent + pinned projects with branch, dirty count, ahead/
  behind, quick actions (terminal here, start agent here, open in Code,
  browse files, review changes). (old: workspace launcher)
- Machine pulse: CPU, memory, disk mini meters; open previews.
- Continue: last 5 agent conversations to resume.

## 3. Terminal (terminal + ptyd)

- Persistent sessions owned by ptyd; survive browser close, network loss,
  `relay serve` restarts/upgrades. Reattach restores screen + scrollback.
  (old: tmux-backed channels; now first-class sessions)
- Tabs of sessions (desktop), session switcher sheet (mobile); rename,
  pin, close (confirm if running), "new terminal here" from anywhere.
- Import existing tmux sessions (attach via a Relay session).
- Mobile: see `docs/dev/TERMINAL_UX.md` (key bar, compose, gestures,
  selection, zoom, keyboard-aware layout). This is the headline feature.
- Desktop keyboard: Mac and Windows/Linux editing shortcuts translate to
  readline/TUI sequences (⌘⌫ kill line, ⌥⌫ kill word, ⌘←/→ home/end,
  ⌥←/→ word jump, Ctrl+⌫ on Windows, etc.). Configurable. (old: partial)
- Paste or drop images/files → chunked upload → path inserted (quoted if
  needed). Works from the phone camera roll. (old)
- Clickable URLs; `localhost:PORT` links open the matching Preview.
- Clickable file paths (`src/app.ts:42`) open in Files/editor.
- Search in scrollback, font size zoom, themes matching the app.
- OSC 52 copy → device clipboard + Relay clipboard history.
- OSC 9 / 777 / BEL → notification + attention state.
- Recording (asciicast v2) for agent sessions by default; replay with
  scrubbing and speed control; download `.cast`.
- `relay` CLI inside sessions: notify, clip, open, preview, ls, attach, run.

## 4. Agents

- Adapters: Claude Code, Codex, Gemini CLI, OpenCode, Kiro, Cursor Agent,
  Grok, pi, Hermes, Amp, Copilot CLI, Aider, Qwen Code, Crush — each with
  detection (installed/version), launch, resume/fork (where supported),
  headless mode, history reader, usage (where transcripts have tokens).
  (old: claude, codex, pi, hermes, grok, opencode, cursor, kiro)
- Live board: running agent sessions paired with their terminal, status
  (working / needs you / idle), task title, cwd, branch, elapsed.
- Launchpad: pick agent → workspace (recent or browse) → optional prompt,
  model, "new git worktree" (branch name) → Start. Opens the terminal.
- History: every past session from disk, grouped by day or project,
  filter by agent / workspace, pin, archive, rename. (old)
- Full-text search across all transcripts (user + assistant text) with
  highlighted snippets; open at the matching message.
- Transcript reader: markdown, collapsible tool calls/results, diffs,
  images, thinking hidden by default; copy message; resume from here.
  (old: plain role/text view)
- Resume and fork with one tap; resume opens in a new terminal. (old)
- Attention hooks: one-click install per agent (Claude Code Notification
  + Stop hooks, Codex notify, Gemini/OpenCode/Kiro/Cursor hooks where
  available) that call `relay hook`. Idle-output heuristic as fallback.
  (old: Claude notify hook, idle watch)
- Usage: cost + tokens by day, agent and model (native parsing, embedded
  price table), plan quotas where readable (Codex rate-limit events,
  Claude via opt-in OAuth usage API). (old: ccusage + quota cards)
- Review: for sessions in git workspaces, "Review changes" opens the diff.

## 5. Workspaces & review

- Auto-discovered git repos under configured roots + cwd of recent agent
  sessions; pin/unpin.
- Review screen: branch, ahead/behind, changed files with +/−, unified
  diff with syntax colouring (mobile-friendly: file list → file diff),
  stage/unstage, discard (confirm), commit message + commit, push/pull
  (runs visibly), worktrees list/create/remove, open a PR with `gh` if
  installed.

## 6. Files

- Browse any folder under the root with breadcrumbs, list/grid, sort,
  hidden toggle (with hidden count), keyboard navigation, multi-select.
  (old)
- Preview: images (with thumbnails), video/audio, PDF, markdown rendered,
  code with syntax highlighting, text; raw download. (old: text/raw)
- Edit text files in CodeMirror (mobile-friendly), save with conflict
  detection.
- Upload files and whole folders (drag & drop, picker, paste), chunked and
  resumable with progress; download files; download folders as zip. (old)
- New folder / new file, rename, move (drag or "Move to…"), copy, delete
  to Trash with restore, empty Trash. (old: permanent delete only)
- Storage usage: folder sizes, largest children. (old)
- Search by name (fast walker) and content (ripgrep when available).
- "Open terminal here", "Start agent here", "Open in Code".

## 7. Code

- code-server (or openvscode-server) started on demand behind Relay auth
  at `/apps/code/`, folder chosen from workspaces or Files; idle stop.
  (old: always-on code-server)

## 8. Desktop

- On-demand X desktop (Xvnc + lightweight WM + panel), started/stopped
  from the UI, idle stop. (old: labwc + Selkies)
- Viewer: noVNC embedded, scale-to-fit or 1:1, quality slider (auto by
  bandwidth), full screen, clipboard sync both ways, mobile controls
  (keyboard toggle, touchpad-style pointer, right-click, scroll).
- Launcher: Chrome, Blender, Files, Terminal, plus configured apps;
  window snapping via WM keybindings. (old: app catalog + window moves)
- Agents can drive the same display (DISPLAY exported to sessions;
  Playwright/Chrome DevTools MCP configured against it) — "watch your agent
  browse".

## 9. Previews

- Detect listening TCP ports (user-owned processes), show process, cwd,
  workspace, framework/title, HTTP check. New dev server → toast
  "Vite on :5173 — Open".
- Open via subdomain `https://5173.<host>` (preferred, requires wildcard
  DNS) or `/p/5173/` (sandboxed). QR code to open on the phone. Label,
  pin, hide. Stop the process.

## 10. System

- Live CPU (per core), memory, swap, disks, network, load, uptime, GPU when
  available; 1-hour history sparklines. (old: health)
- Processes: sort by CPU/memory, search, tree by terminal session, send
  signals (protected processes refused). (old: monitor + kill)
- Services: Relay's units and user systemd units: status, restart, logs.
  (old: units)
- Logs: live tail of journald units or files, filter, pause.

## 11. Notifications

- In-app inbox (bell) with unread count, kinds, deep links.
- Web Push to every subscribed device (PWA on iOS 16.4+, Android, desktop),
  per-kind rules, quiet hours, test button. (old: web push + ring buffer)
- ntfy and generic webhook channels (covers Slack/Discord/email relays).
  (old: ntfy, email, WhatsApp — email/WhatsApp dropped in favour of webhook)
- Smart suppression: no push for a session the user is looking at.

## 12. Command center (⌘K / Ctrl+K)

- Search everything: commands, terminals, agent sessions + transcripts,
  workspaces, files, previews, processes, snippets, notes, settings.
- Actions panel per result (⌘K on a result or → on touch): open, resume,
  kill, rename, copy path, open in Code…
- Arguments inline ("New agent session: [agent] [workspace] [prompt]").
- Built-in extensions: calculator & unit conversions, clipboard history
  (universal clipboard across devices), snippets with {{variables}},
  scratchpad notes, Quick AI (answers from your installed agent CLIs in
  headless mode), system actions (lock, theme, sign out), go-to shortcuts
  (G then T…), recent items.
- Script commands: executables in `~/.config/relay/commands/` with
  Raycast-style metadata comments appear as commands (inline output,
  terminal, or silent modes, with arguments).
- Mobile: full-screen sheet, big targets, opens from the header search.

## 13. Schedules (night shift)

- Create a schedule: name, cron (with presets and a human preview "Every
  weekday at 02:00"), workspace, agent + prompt (headless) or command,
  notify on finish. Run now. History of runs with status and output tail.

## 14. Toolbox

- Catalog of installable tools with status/version: agent CLIs, runtimes
  (Node, Python/uv, Go, Rust, Bun), Chrome, Blender, desktop packages,
  code-server, CLI essentials (ripgrep, fd, fzf, jq, gh, lazygit, btop,
  neovim, tmux), ffmpeg/imagemagick. Install runs visibly in a terminal.
- MCP servers (Playwright, Chrome DevTools, Blender, Context7, filesystem):
  matrix of server × agent; apply/remove using each agent's own CLI or
  config (backed up first).

## 15. Settings

- Security (password, passkeys, TOTP, devices, tokens, activity),
  Notifications, Terminal (font, size, theme, cursor, shortcuts profile,
  key bar layout, copy-on-select), Appearance (Carbon / Paper / Auto,
  reduce motion, density), Workspaces (roots), Agents (hooks, quota
  access), Apps (Code/Desktop), About (version, update available, doctor).
- Onboarding after first sign-in: passkey → notifications → workspace
  roots → tools. Skippable.

## 16. Install & operations

- `curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash`
  installs the binary (checksum verified) and runs `relay setup`: choose
  access (Tailscale / public domain with HTTPS / instant sslip.io domain /
  behind my own proxy), set the password, pick optional components,
  install systemd user units (linger), start, print URL + QR.
- `relay doctor`, `relay update` (self-update with checksum), `relay
  uninstall`, `relay status`, `relay logs`.
- Works on Linux (systemd) amd64/arm64; macOS supported for serve/ptyd
  without systemd (launchd plist provided).

## Dropped from the old gateway (on purpose)

AEM author/publish management, Mac usage sync scripts, OpenUsage bridge,
WhatsApp/email notification channels, fixed per-agent "channels" with
ports, ttyd rescue terminals, the stream governor (replaced by adaptive
VNC quality), Waybar/labwc desktop (replaced by a simpler X desktop).
