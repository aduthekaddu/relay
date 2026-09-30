<div align="center">

# Relay

**Your machine, from any browser.**

One Go binary that turns a Linux server or a Mac into a private workspace you
can open from your phone, tablet or laptop: persistent terminals that work on a
touchscreen, every coding-agent session in one place, files, dev-server
previews, a browser IDE and a remote desktop, behind one login.

[![CI](https://github.com/aduthekaddu/relay/actions/workflows/ci.yml/badge.svg)](https://github.com/aduthekaddu/relay/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/aduthekaddu/relay?include_prereleases&sort=semver)](https://github.com/aduthekaddu/relay/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-ede9e0.svg)](LICENSE)
![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS-0b0b0c.svg)

![Relay on a phone and a laptop: a terminal with an agent waiting for input, and the agents board](docs/assets/hero.png)

```bash
curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
```

[Documentation](docs/index.md) · [Install](docs/getting-started/install.md) · [Security](docs/guides/security.md) · [Contributing](CONTRIBUTING.md)

</div>

---

## Why Relay

Coding agents run for minutes or hours. They stop to ask a question, and then
they wait until you come back to your desk. Relay lets you leave the desk.

- **Your machine keeps working when you close the tab.** Every shell and agent
  runs under a separate session daemon. Closing the browser, losing signal or
  upgrading Relay never kills a running task.
- **A terminal that works on a phone.** A key bar with Esc, Tab, Ctrl and
  arrows. A compose box that supports autocorrect, dictation and paste. Swipe
  to move the cursor. Paste a screenshot and get its file path.
- **It tells you when an agent needs you.** Relay installs hooks for
  supported agents, or watches the terminal itself, and sends a push
  notification to your phone. Tap it to go straight to the prompt.
- **One place for every agent.** Live and past sessions from Claude Code,
  Codex, Gemini CLI, OpenCode and ten more, with full-text search and
  one-tap resume.
- **Nothing to trust but your own box.** Relay is self-hosted, has no
  telemetry and no cloud account, and listens on a single port. You sign in
  with passkeys, and every session can be revoked.

## Feature tour

| Area | What you get |
| --- | --- |
| **Terminal** | Persistent sessions with scrollback replay, a mobile key bar, a compose box, gestures, Mac and Windows editing shortcuts, image paste, clickable paths and URLs, OSC 52 clipboard, and recordings you can replay |
| **Agents** | A live board (working / needs you / idle), a launchpad (agent → folder → prompt, optionally in a new git worktree), history, search, a transcript reader, resume and fork, and usage and quotas |
| **Review** | Git status, diffs that work on a phone, stage, discard, commit, push and pull, and worktrees |
| **Files** | Browse, preview (images, video, PDF, Markdown, code), edit, resumable uploads (including whole folders), zip download, Trash with restore, and storage usage |
| **Previews** | Detects the dev servers you start and opens them over HTTPS on their own subdomain or a sandboxed path. Scan a QR code to open one on your phone |
| **Code** | VS Code in the browser (code-server), started when you open it and stopped when idle |
| **Desktop** | A remote X desktop with Chrome and Blender, clipboard sync and touch controls. Your agents can use the same screen |
| **Command center** | ⌘K / Ctrl+K searches your whole machine. Also: actions, script commands (Raycast-compatible), clipboard history, snippets, notes, a calculator and Quick AI |
| **Notifications** | Web Push to every device (including an iPhone home-screen app), ntfy and webhooks, quiet hours, and no push for the screen you are already looking at |
| **Schedules** | Night-shift runs of agents or commands on a cron schedule, with a notification when they finish |
| **System** | CPU, memory, disks, network and GPU, processes, services and live logs |
| **Toolbox** | One-tap installs of agent CLIs, runtimes, Chrome, Blender and CLI essentials. Also wires MCP servers into every agent |

## Install

On the machine you want to reach (a VPS, a home server, a workstation or a
Mac):

```bash
curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
```

The installer downloads the release for your OS and CPU and verifies its
SHA-256 checksum. It installs `relay` to `~/.local/bin`, then starts
`relay setup`, which asks four things:

1. **How you will reach the machine.** Tailscale (private, recommended), your
   own domain with automatic HTTPS, an instant `sslip.io` address, or behind
   your own reverse proxy or tunnel.
2. **Your username and password.** You can also let setup generate one.
3. **Optional components.** Desktop, Code, agent CLIs and CLI tools.
4. **Whether to start at boot.** On Linux this uses systemd user services. On
   macOS it uses launchd.

It finishes with your URL and a QR code. Open the URL on your phone, sign in,
and add Relay to your home screen.

Step-by-step guides: [what you need](docs/getting-started/what-you-need.md) ·
[install](docs/getting-started/install.md) ·
[choose how to reach it](docs/getting-started/choose-access.md) ·
[VPS from scratch](docs/getting-started/vps-guide.md) ·
[macOS](docs/getting-started/macos.md).

## How it works

```
 your phone / laptop                          your machine
┌─────────────────────┐   HTTPS + WebSocket  ┌──────────────────────────────────────────┐
│ Relay web app (PWA) │ ───────────────────▶ │ relay serve        one port, one login   │
│  terminal, editor,  │                      │  ├─ sign-in: password, passkeys, TOTP    │
│  desktop viewer     │                      │  ├─ /api/v1  JSON + WebSocket            │
└─────────────────────┘                      │  ├─ previews ─▶ 127.0.0.1:<port>         │
                                             │  ├─ /apps/code ─▶ code-server (socket)   │
 Tailscale · your domain · sslip.io ·        │  ├─ desktop ─▶ Xvnc (socket)             │
 Cloudflare Tunnel · your reverse proxy      │  └─ relay.db (SQLite)                    │
                                             │                                          │
 inside any Relay terminal                   │ relay ptyd         the session daemon    │
┌─────────────────────┐   unix socket        │  owns every shell and agent, keeps       │
│ relay notify / clip │ ───────────────────▶ │  scrollback, notices when an agent is    │
│ open / preview / ls │   (owner only)       │  waiting; survives serve restarts        │
└─────────────────────┘                      └──────────────────────────────────────────┘
```

- `relay serve` is the web server, the API and the proxies. `relay ptyd` owns
  every terminal. Both are the same binary. They run as two services, so
  restarting or upgrading the web server leaves your sessions running.
- Agent transcripts are read from where each agent already writes them. Relay
  never modifies them.
- Everything Relay stores lives in one SQLite file and one config file
  (`relay.toml`, mode 0600).

The contributor docs start at [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md).

## Security

Relay gives a browser a shell on your machine, so security is the default:

- **One port and one login.** Everything else (code-server, the desktop, ptyd,
  the CLI) listens only on owner-only unix sockets or on loopback.
- **Strong sign-in.** Passwords are hashed with argon2id. You can add passkeys
  and TOTP codes. Sign-in is rate limited with exponential backoff, and error
  messages never reveal whether a username exists. There are no default
  credentials.
- **Sessions you control.** You can see every signed-in device, revoke one
  or all, and read an audit log. A sign-in from a new device sends you a
  notification.
- **Untrusted content stays contained.** File previews and dev servers run
  sandboxed or on a separate origin, and they never receive your Relay
  cookie. Relay checks the `Origin` header on every WebSocket and every
  state-changing request.
- **No telemetry.** Relay has no account and no cloud service.

The model in plain language: [docs/guides/security.md](docs/guides/security.md).
To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Supported platforms

| Platform | Status |
| --- | --- |
| Linux amd64 / arm64 with systemd (Ubuntu 22.04+, Debian 12+, Fedora 39+, …) | Supported. All features |
| Linux without systemd (containers, WSL2) | Works. You start `relay serve` yourself |
| macOS 13+ (Apple silicon and Intel) | Supported. Uses launchd. No remote desktop or automatic preview detection |
| Windows | Not supported as a host. Any modern browser on Windows works as a client |

| Browser (client) | Notes |
| --- | --- |
| Safari on iOS / iPadOS 16.4+ | Add to Home Screen for push notifications |
| Chrome, Edge, Firefox, Samsung Internet on Android | Full support |
| Chrome, Edge, Firefox and Safari on desktop | Full support |

## Supported agents

Claude Code · Codex · Gemini CLI · OpenCode · Kiro CLI · Cursor Agent ·
Grok · pi · Hermes · Amp · GitHub Copilot CLI · Aider · Qwen Code · Crush

Relay detects each agent that is installed, launches and resumes its
sessions, reads its history, and shows its usage where the transcripts
include token counts. Attention hooks are available for agents that support
them. For the others, Relay watches the terminal for a prompt. See the
[agents guide](docs/guides/agents.md) for what each agent supports.

## Documentation

- **Getting started:** [overview](docs/index.md) ·
  [install](docs/getting-started/install.md) ·
  [first steps](docs/getting-started/first-steps.md)
- **Guides:** [terminal](docs/guides/terminal.md) ·
  [agents](docs/guides/agents.md) ·
  [command center](docs/guides/command-center.md) ·
  [files](docs/guides/files.md) · [previews](docs/guides/previews.md) ·
  [desktop](docs/guides/desktop.md) · [code](docs/guides/code.md) ·
  [notifications](docs/guides/notifications.md) ·
  [schedules](docs/guides/schedules.md) · [toolbox](docs/guides/toolbox.md) ·
  [security](docs/guides/security.md) ·
  [backup and migrate](docs/guides/backup-and-migrate.md) ·
  [troubleshooting](docs/guides/troubleshooting.md) · [FAQ](docs/guides/faq.md)
- **Reference:** [CLI](docs/reference/cli.md) ·
  [configuration](docs/reference/configuration.md) ·
  [HTTP API](docs/reference/api.md) ·
  [keyboard shortcuts](docs/reference/keyboard-shortcuts.md) ·
  [environment variables](docs/reference/environment.md)

## Contributing

Bug reports, fixes, agent adapters, toolbox recipes and docs are all welcome.
[CONTRIBUTING.md](CONTRIBUTING.md) covers the dev setup, the repo layout, the
checks every change must pass, and step-by-step recipes for the common
extensions. Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE) © Relay contributors.
