---
title: FAQ
description: Short answers to common questions about Relay. Covers cost, privacy, security, supported systems, agents, tmux, phones and more.
---

Short answers to the questions people ask most. Each answer links to the
page with the details.

## General

### What is Relay, in one sentence?

A program you install on your own computer or server that lets you use its
terminals, coding agents, files, dev servers, VS Code and desktop from any
browser, including your phone.

### Is it free?

Yes. Relay is open source under the MIT license. You pay only for the
machine you run it on, if it is rented, and for your agents' subscriptions
or API usage.

### Do I need an account with Relay?

No. There is no Relay account and no Relay cloud. You create a username and
password on your own machine during setup.

### Does Relay send any data anywhere?

No telemetry, ever. Relay contacts the internet only for things you set up
or ask for: certificates from Let's Encrypt, push notifications through
your browser's push service, update checks on GitHub, ntfy or webhooks you
configured, and, if you opt in, reading your Claude plan usage from
Anthropic. Agents you run talk to their own providers, as they always do.

### Can several people use one Relay?

No. Relay is built for one person: the owner of the machine account it runs
as. Everyone who signs in has the same full access. For a team, give each
person their own machine or their own user account with their own Relay.

## Setup

### Which systems can run Relay?

Linux (amd64 or arm64) with systemd, which is fully supported, and macOS 13
or newer. Linux without systemd works if you start `relay serve` yourself.
Windows cannot run Relay as a host, but any Windows browser can use it. See
[What you need](../getting-started/what-you-need.md).

### Which access option should I choose?

Tailscale if you can install its app on your devices, because it is the most
private. Your own domain if you want to open Relay from any browser.
sslip.io to try it in two minutes. See
[Choose how to reach your machine](../getting-started/choose-access.md).

### Can I run Relay on my home computer and reach it from outside?

Yes. Use Tailscale or a Cloudflare Tunnel. Neither needs port forwarding on
your router.

### Does it need root?

No. Relay runs as your normal user. `sudo` is needed only for optional
steps, and setup asks first: turning on lingering, allowing port 443, and
installing system packages such as the desktop.

## Using it

### Do my terminals really survive closing the browser?

Yes. They run under Relay's session daemon on the machine, not in your
browser. They even survive restarts and updates of the Relay web server.
They do not survive a reboot of the machine, but Relay can restore them
afterwards. See [Terminal](terminal.md).

### Is this a replacement for tmux?

For most people, yes: sessions persist, several devices can attach to one
session, and scrollback is replayed. You can keep using tmux inside Relay,
and Relay can attach to your existing tmux sessions.

### Which agents are supported?

Claude Code, Codex, Gemini CLI, OpenCode, Kiro CLI, Cursor Agent, Grok, pi,
Hermes, Amp, GitHub Copilot CLI, Aider, Qwen Code and Crush. See
[Agents](agents.md).

### Does Relay use my agent subscriptions or need API keys?

Relay runs the agent CLIs you have installed and signed in to, with their
own credentials. Quick AI and scheduled agent runs use them in the same
way. Relay has no AI model of its own.

### Does Relay change my agents' files?

Relay reads transcripts and never writes them. It changes an agent's config
file only when you ask it to install attention hooks or MCP servers, and it
makes a backup first.

### How does it know an agent needs me?

Through hooks (for agents that support them) or by watching the terminal
for bells, notifications and known prompts. See
[Get told when an agent needs you](agents.md#get-told-when-an-agent-needs-you).

### Do I have to keep a browser tab open to get notifications?

No. Once push is turned on for a device, notifications arrive even when
Relay is closed. On iPhone, Relay must be added to the Home Screen. See
[Notifications](notifications.md).

### Does it work on a slow connection?

Yes. Terminals send only the changes to the screen, reconnect on their own
and keep what you typed while offline. The desktop viewer lowers its
quality to match your bandwidth.

### Can I use a hardware keyboard with my iPad or phone?

Yes. Relay handles hardware keyboards and translates editing shortcuts the
way it does on a computer. See
[Keyboard shortcuts](../reference/keyboard-shortcuts.md).

## Security

### Is it safe to put a shell on the internet?

Relay is designed for it: argon2id password hashing, passkeys, optional
TOTP, strict rate limits, revocable sessions, an activity log, and sandboxing
for untrusted content. The safest setup still keeps the sign-in page off
the public internet with Tailscale or Cloudflare Access. See
[Security](security.md).

### What happens if I lose my phone?

Sign in from another device, go to **Settings → Security → Devices** and
sign out the lost phone. Remove its passkey under **Passkeys**. If it also
held your only authenticator, see
[I am locked out](troubleshooting.md#i-am-locked-out).

### How do I report a vulnerability?

Privately, as described in
[SECURITY.md](https://github.com/aduthekaddu/relay/blob/main/SECURITY.md).
Please don't open a public issue.

## Next steps

- [Troubleshooting](troubleshooting.md)
- [Install Relay](../getting-started/install.md)
