# Changelog

All notable changes to Relay are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before 1.0, minor versions may include breaking changes, and they are called
out under **Changed**.

## [Unreleased]

The first public release, planned as **v0.1.0**.

### Added

**Install and operations**

- One-line installer (`curl -fsSL …/scripts/install.sh | bash`) for Linux and
  macOS, amd64 and arm64, with SHA-256 checksum verification. Installs into
  `~/.local/bin` without root.
- `relay setup` wizard, with flags for unattended installs. Access modes:
  Tailscale, your own domain with automatic Let's Encrypt HTTPS, an instant
  `sslip.io` address, behind your own proxy or tunnel, or local only. Also
  creates the account, installs optional components and systemd user
  services (with linger) or launchd agents, and prints the URL and a QR
  code.
- `relay doctor` (with `--json`), `relay status`, `relay logs [-f]`,
  `relay update [--check] [--version] [--all]` (keeps terminals running),
  and `relay uninstall [--purge]`.
- Two processes from one binary: `relay serve` (web server, API, proxies)
  and `relay ptyd` (session daemon), so that restarts and upgrades never
  kill a running shell or agent.

**Sign-in and security**

- First-run account creation, with no default credentials. Passwords are
  hashed with argon2id.
- Passkeys (discoverable credentials and autofill), optional TOTP second
  factor, and "Keep me signed in" (30 days) versus normal (12 hours)
  sessions.
- Per-IP and global rate limits with exponential backoff, and generic
  error messages.
- A device list with revoke-one and revoke-all-others. A password change
  signs out other devices.
- API tokens (`rly_…`, shown once, hashed at rest, with last-used time),
  and `relay token`.
- Activity log of sign-ins, failures, token use, revocations and
  destructive actions, plus a notification for sign-ins from new devices.
- `relay passwd` to set the password from the machine.
- Strict `Origin` checks for unsafe requests and every WebSocket. File
  previews and dev-server previews are sandboxed and never receive the
  session cookie.

**Terminal**

- Persistent sessions with scrollback replay on reconnect. Import of
  existing tmux sessions, and Restore after a reboot.
- A mobile terminal with a customisable key bar (sticky Ctrl/Alt,
  alternates on long-press), swipe-to-move-cursor, a compose bar
  (autocorrect, dictation, snippets), direct mode for TUIs, native
  selection, pinch zoom and keyboard-aware layout.
- Mac and Windows/Linux editing shortcut translation (⌘⌫, ⌥⌫, ⌘←/→,
  Ctrl+⌫ …), with profiles.
- Paste or drop images and files. Chunked, resumable upload, with the path
  inserted.
- Clickable URLs (`localhost` links open previews) and clickable file paths.
- OSC 52 clipboard. OSC 9/777 and the bell mark a session as needing you.
- asciicast v2 recordings of agent sessions, with replay, scrubbing, speed
  control and download.
- The `relay` CLI inside terminals: `notify`, `clip`, `open`, `preview`,
  `ls`, `attach`, `run` and `hook`.

**Agents and workspaces**

- Adapters for Claude Code, Codex, Gemini CLI, OpenCode, Kiro CLI, Cursor
  Agent, Grok, pi, Hermes, Amp, GitHub Copilot CLI, Aider, Qwen Code and
  Crush: detection, launch, resume and fork where supported, headless
  mode, and history.
- A live board (working / needs you / idle), and a launchpad with an
  optional new git worktree.
- History from every agent's own files, full-text search, and a
  transcript reader with collapsible tool calls, diffs and images.
- Attention hooks installed with one click (with backups), plus a terminal
  heuristic as a fallback.
- Usage and cost by day, agent and model from an embedded price table.
  Codex rate-limit quotas, and opt-in Claude plan usage.
- Workspaces discovered from configured roots, and a review screen:
  status, diffs, stage, unstage, discard, commit, push, pull, worktrees,
  and opening a PR with `gh`.

**Files, previews, code and desktop**

- File manager: list and grid views, previews (images, video, audio, PDF,
  Markdown, code), a text editor with conflict detection, resumable
  file and folder uploads, zip download, Trash with restore, storage usage,
  and name and content search.
- Previews of dev servers: automatic detection with toasts, subdomain or
  sandboxed path mode, QR codes, labels, pinning and hiding.
- Code: code-server started on demand behind the Relay login, with idle
  stop. Also on-demand custom `[[apps]]`.
- Desktop: an on-demand X desktop (Xvnc, Openbox, tint2) in the browser, with
  adaptive quality, two-way clipboard, touch controls, and a launcher for
  Chrome, Blender and custom apps. Agents can drive it through `DISPLAY`.

**Notifications, command center, schedules and toolbox**

- Web Push to every device (including iOS 16.4+ home-screen apps), ntfy
  and webhook channels, per-kind rules, quiet hours, and no push for the
  screen you are looking at.
- Command center (⌘K / Ctrl+K): federated search across the machine,
  actions, inline arguments, go-to shortcuts, a calculator and unit
  conversions, a universal clipboard history, snippets with `{{variables}}`,
  notes, Quick AI through your installed agent CLIs, and Raycast-compatible
  script commands from `~/.config/relay/commands/`.
- Schedules: cron runs of headless agents or commands, with run history and
  notifications.
- Toolbox: one-tap installs of agent CLIs, runtimes, browsers, desktop
  packages, creative tools and CLI essentials, plus an MCP server × agent
  matrix with apply and remove.
- System: live metrics with a 1-hour history, processes (with protected
  processes refused), services and live logs.

**App**

- An installable PWA with the "Signal" design system: Carbon (dark), Paper
  (light) and Auto themes, reduced motion, and mobile-first layouts.
- Onboarding after the first sign-in: passkey, notifications, workspace
  roots and tools.

[Unreleased]: https://github.com/aduthekaddu/relay/commits/main
