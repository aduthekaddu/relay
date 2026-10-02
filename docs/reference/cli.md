---
title: Command line (relay)
description: Every relay command and flag. Covers setup and operations, the server and session daemon, account management, and the commands you use inside Relay terminals.
---

`relay` is one binary. It runs the server and the session daemon, sets
itself up, checks its own health, and gives you small commands to use
inside Relay terminals. This page lists every command and flag. Run
`relay help` for the list on your installed version, and
`relay help <command>` for one command.

```text
relay <command> [flags] [arguments]
```

**Exit codes:** `0` success · `1` the command failed (the reason is printed
to stderr) · `2` the command line was wrong (unknown command or flag).

## Overview

| Group | Commands |
| --- | --- |
| Setup and operations | [`setup`](#relay-setup) · [`doctor`](#relay-doctor) · [`status`](#relay-status) · [`logs`](#relay-logs) · [`update`](#relay-update) · [`uninstall`](#relay-uninstall) |
| Server | [`serve`](#relay-serve) · [`ptyd`](#relay-ptyd) |
| Account | [`passwd`](#relay-passwd) · [`token`](#relay-token) |
| Inside terminals | [`notify`](#relay-notify) · [`clip`](#relay-clip) · [`open`](#relay-open) · [`preview`](#relay-preview) · [`ls`](#relay-ls) · [`attach`](#relay-attach) · [`run`](#relay-run) · [`hook`](#relay-hook) |
| Other | [`version`](#relay-version) · [`help`](#relay-help) |

The commands in "Inside terminals" talk to the running server through the
local control socket. Inside a Relay terminal, they find it through
`RELAY_SOCKET`. Elsewhere on the machine, they use the default socket
path. Only your user can use the socket, so no password or token is needed.

## Setup and operations

### `relay setup`

The interactive setup wizard: access mode, account, optional components,
services and the final URL. [Install Relay](../getting-started/install.md)
walks through it. Running it again is safe: it offers to keep your
existing `relay.toml`. Every question has a flag for unattended installs.

```text
relay setup [flags]
```

| Flag | Meaning |
| --- | --- |
| `--access MODE` | `tailscale`, `domain`, `sslip`, `proxy` or `local` |
| `--domain NAME` | Domain for `domain` mode, for example `relay.example.com` |
| `--email ADDRESS` | Contact email for Let's Encrypt (`domain` and `sslip` modes) |
| `--listen ADDR` | Listen address, overriding the mode's default (for example `127.0.0.1:7777`) |
| `--user NAME` | Username for the account |
| `--password-stdin` | Read the password from standard input instead of prompting |
| `--components LIST` | Comma-separated optional components: `desktop`, `code`, `agents`, `tools` |
| `--yes` | Accept defaults and do not prompt. A password is generated and printed if none is given |
| `--no-systemd` | Do not install or start services (launchd on macOS). Only write the config |
| `--units-dir DIR` | Write service files to `DIR` instead of the default user unit folder |

```bash
printf '%s\n' "$RELAY_PASSWORD" | relay setup --yes --access tailscale --user me --password-stdin
```

### `relay doctor`

Checks the installation and prints ✓ / ⚠ / ✗ lines with a fix hint for each
problem. [Troubleshooting](../guides/troubleshooting.md#read-relay-doctor-output)
explains every check.

| Flag | Meaning |
| --- | --- |
| `--json` | Print the results as JSON |

### `relay status`

A short summary: whether the server and the session daemon are running, the
URL, the version and the number of sessions.

### `relay logs`

Shows the logs of both services (`relay` and `relay-ptyd`). On Linux, it
reads them from the systemd journal.

| Flag | Meaning |
| --- | --- |
| `-f` | Follow: keep printing new lines until Ctrl+C |

### `relay update`

Downloads the latest release from GitHub and verifies its SHA-256 checksum.
It then replaces the binary in place and restarts the web server. Your
terminals keep running, because the session daemon is not restarted unless
you ask.

| Flag | Meaning |
| --- | --- |
| `--check` | Only report whether a newer version exists |
| `--version VERSION` | Install a specific version, for example `v0.1.0` |
| `--all` | Also restart the session daemon. **This ends every running terminal** |

On Linux with automatic HTTPS on port 443, grant the port permission again
after updating. See [Relay cannot use port 443](../guides/troubleshooting.md#relay-cannot-use-port-443).

### `relay uninstall`

Stops, disables and removes Relay's services. Your config and data are kept
unless you add `--purge`.

| Flag | Meaning |
| --- | --- |
| `--purge` | Also delete Relay's config, data and cache folders (asks for confirmation). Never touches your own files or agents' data |

## Server

### `relay serve`

Runs the web server in the foreground. The `relay` service runs this
command. If the session daemon isn't running and systemd isn't available,
`relay serve` starts one in the background, so `relay serve` alone works on
a laptop.

| Flag | Meaning |
| --- | --- |
| `--listen ADDR` | Override [`server.listen`](configuration.md#server), for example `127.0.0.1:7777` |
| `--debug` | Verbose logging, including one line per request (never bodies or secrets) |
| `--open` | Print the URL to open once the server is ready |

### `relay ptyd`

Runs the session daemon in the foreground. The `relay-ptyd` service runs
this command. It owns every terminal session, and it keeps running when
the web server restarts.

| Flag | Meaning |
| --- | --- |
| `--socket PATH` | Listen on this unix socket instead of the default `ptyd.sock` in the runtime folder |

## Account

### `relay passwd`

Sets a new password for your account. It asks twice, with hidden input, and
the password must be at least 10 characters. The command writes to Relay's
database directly, so it works whether or not the server is running.
By default, it signs out every browser session. API tokens and passkeys stay valid.
Ordinary password changes leave TOTP enabled.

| Flag | Meaning |
| --- | --- |
| `--user NAME` | Create the account with this name, or rename the existing account |
| `--stdin` | Read the new password from standard input, one line |
| `--keep-sessions` | Preserve browser sessions during an ordinary password change. Cannot be combined with `--reset-totp` |
| `--reset-totp` | Recover an existing account: replace the password, clear active and pending TOTP, and sign out every browser session |

Recovery requires a shell as the OS user that owns Relay's database, a private
`0700` data directory and regular, singly linked `0600` database files owned by
that user. Symlinks and insecure permissions are refused without repair.
Run as the service's owner, with the same `RELAY_HOME` or XDG data directory.
A different data directory cannot recover the installed account. Recovery
never creates a missing account or uses `RELAY_SOCKET`.

Password replacement, TOTP reset, browser session revocation and the
`totp.recover` activity entry commit together. On failure they roll back together.
The server can remain running when it runs the same recovery-aware Relay version
as the CLI. If an older binary is still serving, stop that server with its
service manager before recovery and restart it with the updated binary afterward.
Stale control sockets do not affect recovery.
Old cookies fail on their next request. Authenticated API WebSockets close
within three seconds; see the [socket scope and limits](../dev/AUTH.md#sessions-and-cookies).
API tokens and passkeys remain valid. Recovery does not kill terminals or sign
you in. Sign in with the new password and enroll TOTP again.

Follow the [lockout recovery guide](../guides/troubleshooting.md#i-am-locked-out).

### `relay token`

Manages API tokens for the [HTTP API](api.md).

```text
relay token create <name>     # prints the new token once (rly_…)
relay token list              # id, name, created, last used
relay token revoke <id>       # revoke immediately
```

## Inside terminals

### `relay notify`

Sends a notification to your devices and the in-app inbox. If you give no
message, it reads the message from standard input. Inside a Relay terminal,
the notification links back to that terminal.

```text
relay notify [-t title] [--kind kind] [--link url] [message…]
```

| Flag | Meaning |
| --- | --- |
| `-t TITLE` | Notification title (default: the session name) |
| `--kind KIND` | `custom` (default), `done`, `attention`, or another [kind](../guides/notifications.md#kinds-of-notifications) |
| `--link URL` | What the notification opens. Either a Relay path such as `/files?path=~/report.pdf`, or a full URL |

```bash
make release && relay notify -t Release --kind done "v1.4 published"
```

### `relay clip`

Reads and writes the universal clipboard, which is shared by all your
devices.

```text
command | relay clip       # copy standard input
relay clip -p              # print the latest entry
relay clip --list          # list recent entries
```

| Flag | Meaning |
| --- | --- |
| `-p`, `--paste` | Print the latest clipboard entry to standard output |
| `--list` | List recent entries |

Entries are limited to 256 KB.

### `relay open`

Opens a file in Relay on the device you are using: in the Files preview, or
in the editor at a line.

```text
relay open <path[:line]>
```

Relative paths are resolved from the current folder. The file must be
inside [`files.root`](configuration.md#files).

```bash
relay open src/server/router.go:120
```

### `relay preview`

Prints the preview URL for a port, with a QR code you can scan with your
phone.

```text
relay preview <port>
```

### `relay ls`

Lists terminal sessions: id, name, status, folder, attached clients and age.

| Flag | Meaning |
| --- | --- |
| `--json` | Print the sessions as JSON |

### `relay attach`

Attaches this terminal to another Relay session, by id or name. The screen
and scrollback are replayed, and the size follows your window. Press
**Ctrl+\\** then **d** to detach. The session keeps running.

```text
relay attach <id|name>
```

### `relay run`

Starts a command in a new background session and prints its id. The
session appears in the Terminal list like any other.

```text
relay run [--name NAME] [--cwd DIR] [--attach] -- command [args…]
```

| Flag | Meaning |
| --- | --- |
| `--name NAME` | Session name (default: the command name) |
| `--cwd DIR` | Folder to start in (default: the current folder) |
| `--attach` | Attach to the new session right away |

```bash
relay run --name devserver --cwd ~/code/app -- pnpm dev
```

### `relay hook`

Called by agent attention hooks. It reads the agent's hook data (JSON) from
standard input and tells Relay that the session needs you, or that it
finished. You normally never run it yourself: **Settings → Agents →
Install hooks** sets it up. It always exits with status 0 within about two
seconds, so it can never block an agent.

```text
relay hook <agent> <event>
```

Examples of what Relay installs: `relay hook claude notification`,
`relay hook claude stop`, `relay hook codex notify`.

## Other

### `relay version`

Prints the version, commit, platform and Go version. `relay --version` and
`relay -v` do the same.

### `relay help`

Lists the commands. `relay help <command>` shows one command's usage and
flags. `relay <command> -h` does the same.

## Next steps

- [Configuration reference](configuration.md)
- [Environment variables](environment.md)
- [Terminal → the relay command](../guides/terminal.md#use-the-relay-command-inside-a-terminal)
