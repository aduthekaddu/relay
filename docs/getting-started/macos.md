---
title: Relay on macOS
description: Run Relay on a Mac so you can reach your Mac's terminals, agents and files from your phone. Covers install, launchd, sleep settings and what differs from Linux.
---

Relay runs on macOS 13 (Ventura) and newer, on Apple silicon and on Intel.
With it, you can reach your Mac's terminals, coding agents, files and
VS Code from your phone or another computer. Installation is the same
one-line command as on Linux. Relay starts at login through launchd, the
macOS service manager. The main thing to get right is keeping the Mac awake.

## What works on macOS

| Feature | macOS |
| --- | --- |
| Terminals, persistent sessions, recordings | ✓ |
| Agents: board, history, search, resume, hooks, usage | ✓ |
| Files, uploads, Trash | ✓ (Relay's Trash follows the freedesktop layout; items do not appear in the Finder Trash) |
| Code (code-server) | ✓ |
| Notifications, command center, schedules, toolbox | ✓ |
| Previews | Open any port with `/p/<port>/` or `relay preview <port>`. **Automatic detection** of new dev servers is Linux-only in v0.1 |
| Remote desktop | Not available. It uses an X server. Use macOS **Screen Sharing** over Tailscale instead |
| System: processes, metrics | ✓ Basic. Service management covers Relay's own launchd agents |

## Install

1. Open **Terminal** (Applications → Utilities).
2. Run:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
   ```

3. When setup asks how you will reach the Mac, choose **Tailscale**. A Mac
   at home or in an office rarely has a public IP address, and Tailscale
   works without opening ports on your router. Install the Tailscale app
   from the Mac App Store first, and follow
   [Use Tailscale](choose-access.md#use-tailscale-recommended).
4. Setup installs two launch agents in `~/Library/LaunchAgents/`: one for the
   session daemon and one for the web server. They start when you log in and
   restart if they stop.

The binary goes to `~/.local/bin/relay`. Relay's config is in
`~/.config/relay/relay.toml` and its data is in `~/.local/share/relay/`,
the same places as on Linux.

:::note[Permissions]
The first time a Relay terminal opens a protected folder, such as
Documents, Desktop or Downloads, macOS may ask whether Relay may access it.
Allow it, or grant **Full Disk Access** to `relay` in **System Settings →
Privacy & Security** if you want agents to work anywhere in your home
folder.
:::

## Keep the Mac awake

Relay stops responding while the Mac sleeps. For a Mac that should always be
reachable, such as a desktop Mac or a Mac mini:

1. Open **System Settings → Energy** (on laptops: **Battery → Options**).
2. Turn on **Prevent automatic sleeping when the display is off**. On
   laptops, this setting only applies on power.
3. Optionally turn on **Wake for network access**.

For a laptop, keep it plugged in and awake only while you need it:

```bash
caffeinate -dimsu
```

This keeps the Mac awake until you press Ctrl+C. Closing the lid still
puts most MacBooks to sleep unless an external display is connected.

## Start, stop and check Relay

```bash
relay status          # is it running, and where
relay doctor          # full health check with fix hints
relay logs -f         # follow the logs
```

To stop Relay until the next login, unload the launch agents. `relay
uninstall` removes them for good and keeps your data. Commands such as
`systemctl` in the rest of these docs are Linux-only. On a Mac, use the
`relay` commands above.

## Differences from Linux

- **No systemd, no linger.** Relay runs while you are logged in to the Mac.
  Enable automatic login if the Mac should be reachable after a power cut
  (System Settings → Users & Groups).
- **Ports.** In Tailscale and proxy modes, Relay listens on
  `127.0.0.1:7777`, so no special permissions are needed. Binding port 443
  directly on a Mac is not recommended.
- **Toolbox recipes** declare which platforms they support. The Toolbox
  shows only the ones that work on macOS. Desktop packages, for example, are
  Linux-only.
- **The runtime folder** (sockets) is in `$TMPDIR/relay-<uid>/`, because
  macOS has no `XDG_RUNTIME_DIR`.

## Next steps

- [First steps](first-steps.md)
- [Terminal](../guides/terminal.md)
- [Troubleshooting](../guides/troubleshooting.md)
