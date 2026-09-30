---
title: What you need
description: A machine to run Relay on, a browser to open it in, and a way to reach one from the other.
---

To use Relay you need three things: a machine that stays on, a browser, and
a way for the browser to reach the machine. This page helps you pick each
one. None of them has to cost money, and you can change your mind later.

## A machine to run Relay on

Relay runs on the machine whose terminals, files and agents you want to
use. Any of these works:

| Option | Good for | Things to know |
| --- | --- | --- |
| **A VPS** (a rented virtual server) | Agents that run all night, reaching it from anywhere, a machine that never sleeps | A few dollars to a few tens of dollars a month. See the [VPS guide](vps-guide.md) |
| **A home server or an always-on desktop** (Linux) | Using hardware you already own, big disks, a GPU | Must stay powered on. Reaching it from outside your home needs Tailscale or a tunnel |
| **Your Mac** | Working on your own laptop or Mac mini from your phone | The Mac must not go to sleep. See [Relay on macOS](macos.md) |

Minimum and recommended sizes:

| | Minimum | Comfortable for several agents |
| --- | --- | --- |
| CPU | 1 vCPU | 2–4 vCPUs |
| Memory | 1 GB | 4–8 GB (agents, language servers and Chrome add up) |
| Disk | 2 GB free | 20 GB or more |
| OS | Linux (amd64 or arm64) with systemd, or macOS 13+ | Ubuntu 24.04 LTS or Debian 12 |

Relay itself is small: one binary with no database server to install. Most
of the memory goes to the things you run with it, such as agents, builds,
code-server and the desktop.

:::note
Relay is for **one person**. You sign in as the owner of the machine, and
everything runs as your user. It is not a way to share a machine with a team.
:::

## A browser

Any modern browser works as the client, so there is nothing to install on
your phone or laptop:

- **iPhone and iPad:** Safari on iOS/iPadOS 16.4 or newer. Push
  notifications need Relay added to your Home Screen. [First steps](first-steps.md)
  shows how.
- **Android:** Chrome, Edge, Firefox or Samsung Internet.
- **Desktop:** Chrome, Edge, Firefox or Safari.

## A way to reach the machine

The browser has to reach Relay over HTTPS. HTTPS encrypts the connection,
and passkeys and push notifications only work over HTTPS. Choose one of
these:

| Way | In one sentence | Choose it when |
| --- | --- | --- |
| **Tailscale** (recommended) | A private network between your own devices. Relay is invisible to the rest of the internet | You want the safest setup and don't mind installing the Tailscale app on your phone |
| **Your own domain** | `https://relay.example.com` with a free certificate that Relay gets for you | You own a domain and the machine has a public IP address |
| **Instant sslip.io address** | `https://203-0-113-10.sslip.io`, a free address built from your server's IP. No domain needed | You want to try Relay on a VPS in two minutes |
| **Cloudflare Tunnel or your own proxy** | Another program handles HTTPS and forwards to Relay | You already use Cloudflare, Caddy or nginx, or your machine has no public IP |

[Choose how to reach your machine](choose-access.md) compares them and gives
exact steps for each.

## Also useful, but optional

- **A domain name.** You need one for "your own domain" mode and for dev-server
  previews on their own subdomain. You can buy one from any domain registrar
  for about 10 USD a year.
- **The coding agents you use,** such as Claude Code or Codex. Relay can
  install them for you from the [Toolbox](../guides/toolbox.md).
- **SSH access** to the machine. You need it once, to run the installer.
  After that, Relay's own terminal is enough.

## Next steps

- [Choose how to reach your machine](choose-access.md)
- [Install Relay](install.md)
- No machine yet? Follow the [VPS guide](vps-guide.md).
