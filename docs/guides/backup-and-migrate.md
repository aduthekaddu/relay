---
title: Backup and migrate
description: What Relay stores and where, how to back it up safely, and how to move Relay to a new machine without losing your account, passkeys or history.
---

Relay keeps everything it owns in two folders: a config folder and a data
folder. Back up those two, and you can restore Relay or move it to another
machine with your account, passkeys, settings, snippets, notes, schedules
and recordings intact. Your projects and your agents' own history live
elsewhere, and you back them up as usual.

## What Relay stores and where

| What | Default location | Back up? |
| --- | --- | --- |
| Config: `relay.toml` | `~/.config/relay/relay.toml` | **Yes** |
| Your script commands | `~/.config/relay/commands/` | **Yes** |
| Database: `relay.db` (account, passkeys, sessions, tokens, activity log, notifications and push subscriptions, clipboard, snippets, notes, schedules, preview labels, agent index) | `~/.local/share/relay/relay.db` (plus `-wal` and `-shm` files while running) | **Yes** |
| Terminal recordings | `~/.local/share/relay/recordings/` | Optional |
| Uploaded files (pasted images and so on) | `~/.local/share/relay/uploads/` | Optional |
| Session list (for "Restore" after a reboot) | `~/.local/share/relay/ptyd/` | Optional |
| Code's settings and extensions | `~/.local/share/relay/code/` | Optional (can be large) |
| Desktop Chrome profile | `~/.local/share/relay/desktop/` | Optional (can be large) |
| Thumbnails and other caches | `~/.cache/relay/` | No |
| Sockets | `$XDG_RUNTIME_DIR/relay/` | No |

If you set `RELAY_HOME`, everything lives under that one folder instead:
`$RELAY_HOME/config`, `$RELAY_HOME/data`, `$RELAY_HOME/cache` and
`$RELAY_HOME/run`. See [Environment variables](../reference/environment.md).

**Not stored by Relay:** your projects, and your agents' transcripts and
settings (for example `~/.claude/`, `~/.codex/`, `~/.gemini/`). Relay reads
those in place, so back them up with the rest of your home folder if you
want to keep your agent history.

:::caution
`relay.toml` and `relay.db` contain your password hash, your TOTP secret and
the keys for push notifications. Store backups somewhere private, and
encrypt them if they leave the machine.
:::

## Back up

The safest way is to briefly stop the web server, copy the folders, and
start it again. Stopping `relay` does **not** stop your terminals or agents,
because they belong to `relay-ptyd`.

```bash
systemctl --user stop relay
tar -czf ~/relay-backup-$(date +%F).tar.gz -C ~ \
  .config/relay \
  .local/share/relay/relay.db \
  .local/share/relay/recordings
systemctl --user start relay
```

To back up without stopping anything, use SQLite's online backup for the
database:

```bash
sqlite3 ~/.local/share/relay/relay.db ".backup '$HOME/relay-db-backup.db'"
cp ~/.config/relay/relay.toml ~/relay.toml.backup
```

Copy the result off the machine, for example with `scp` or your usual
backup tool. On a VPS, provider snapshots are an easy extra safety net.

## Restore on the same machine

```bash
systemctl --user stop relay
tar -xzf ~/relay-backup-2026-10-01.tar.gz -C ~
systemctl --user start relay
relay doctor
```

## Move Relay to a new machine

1. **On the new machine**, install Relay without running setup:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash -s -- --no-setup
   ```

2. **On the old machine**, make a backup as shown above and copy it over:

   ```bash
   scp ~/relay-backup-2026-10-01.tar.gz me@new-machine:
   ```

3. **On the new machine**, unpack it:

   ```bash
   tar -xzf ~/relay-backup-2026-10-01.tar.gz -C ~
   ```

4. **Run setup** and choose **keep** when it finds your existing
   `relay.toml`. Setup installs the services and starts Relay with your
   config:

   ```bash
   relay setup
   ```

5. **Move the address.** If you use your own domain, change its DNS
   **A record** to the new machine's IP. Once DNS updates, Relay gets a new
   certificate by itself. With Tailscale, run `tailscale serve` on the new
   machine, as in [Use Tailscale](../getting-started/choose-access.md#use-tailscale-recommended).
6. **Check it:** `relay doctor`, then sign in.
7. **Optional:** copy your agents' folders (for example `~/.claude/projects`,
   `~/.codex/sessions`) to keep your history. Install your agents from the
   [Toolbox](toolbox.md), and select **Install hooks** again if their config
   files were not copied.

### What carries over

| Carries over | Does not carry over |
| --- | --- |
| Account, password, TOTP | Running terminals and agents (processes cannot move between machines) |
| Passkeys, **if the address stays the same** | Passkeys, if the address changes: add new ones |
| Push notifications on your devices, if the address stays the same | Push, if the address changes: turn it on again on each device |
| Settings, snippets, notes, schedules, clipboard, API tokens | Installed tools: reinstall from the Toolbox |
| Device sessions (you stay signed in) | Code extensions and the desktop Chrome profile, unless you copied those folders |

:::tip
After a move, go to **Settings → Security → Devices** and sign out anything
you don't recognise. If the old machine is being decommissioned, run
`relay uninstall --purge` there once the new one works.
:::

## Next steps

- [Security](security.md)
- [Troubleshooting](troubleshooting.md)
- [Configuration reference](../reference/configuration.md)
