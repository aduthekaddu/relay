---
title: Relay documentation
description: Relay turns your computer or server into a private workspace you can open from any browser. Start here.
---

Relay turns one machine, such as a VPS, a home server or a Mac, into a
private workspace that you open in a browser. You get terminals that keep
running after you close the tab, every coding-agent session in one place,
your files, previews of your dev servers, VS Code in the browser and a full
remote desktop. All of it sits behind one login that only you have.

Relay is one program. You install it with one command, and it runs on your
machine. There is no Relay account and no cloud service, and it sends no
telemetry.

## Where to start

If you have never set up a server before, follow these pages in order:

1. [What you need](getting-started/what-you-need.md): a machine, a browser,
   and a way to reach the machine.
2. [Choose how to reach your machine](getting-started/choose-access.md): Tailscale,
   your own domain, an instant address, or a tunnel.
3. [Install Relay](getting-started/install.md): one command, then a few
   questions.
4. [First steps](getting-started/first-steps.md): sign in, add Relay to your
   phone's home screen, set up a passkey and turn on notifications.

If you do not have a machine yet, the [VPS guide](getting-started/vps-guide.md)
takes you from creating an account with a hosting provider to a working Relay.
Using a Mac? Read [Relay on macOS](getting-started/macos.md).

## What you can do

| I want to… | Read |
| --- | --- |
| Use a terminal on my phone without fighting the keyboard | [Terminal](guides/terminal.md) |
| Start, watch, resume and search coding agents | [Agents](guides/agents.md) |
| Get a push notification when an agent needs me | [Notifications](guides/notifications.md) |
| Find anything on my machine with one shortcut | [Command center](guides/command-center.md) |
| Browse, upload, edit and download files | [Files](guides/files.md) |
| Open my dev server on my phone | [Previews](guides/previews.md) |
| Use VS Code in the browser | [Code](guides/code.md) |
| Use Chrome or Blender on the machine, or watch an agent use them | [Desktop](guides/desktop.md) |
| Run agents or scripts overnight | [Schedules](guides/schedules.md) |
| Install agent CLIs and tools, and set up MCP servers | [Toolbox](guides/toolbox.md) |
| Understand and harden security | [Security](guides/security.md) |
| Back up Relay or move it to a new machine | [Backup and migrate](guides/backup-and-migrate.md) |
| Fix something that is not working | [Troubleshooting](guides/troubleshooting.md) · [FAQ](guides/faq.md) |

## Reference

- [Command line (`relay …`)](reference/cli.md)
- [Configuration (`relay.toml`)](reference/configuration.md)
- [HTTP API](reference/api.md)
- [Keyboard shortcuts](reference/keyboard-shortcuts.md)
- [Environment variables](reference/environment.md)

## How Relay is built

Relay is two processes from the same binary. `relay serve` is the web server:
it handles sign-in, the API and the proxies. `relay ptyd` is the session
daemon: it owns every terminal. Because they are separate, restarting or
updating the web server never stops a shell or an agent in the middle of a
task. Everything Relay stores is on your machine, in one SQLite database and
one config file.

Developers can read the [architecture overview](https://github.com/aduthekaddu/relay/blob/main/docs/dev/ARCHITECTURE.md) and the
[contributing guide](https://github.com/aduthekaddu/relay/blob/main/CONTRIBUTING.md).

## Next steps

- [What you need](getting-started/what-you-need.md)
- [Install Relay](getting-started/install.md)
