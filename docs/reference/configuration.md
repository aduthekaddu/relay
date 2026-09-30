---
title: Configuration (relay.toml)
description: Every key in relay.toml, with its type, default and meaning, plus where the file lives and how environment variables override it.
---

Relay has one config file, `relay.toml`, in [TOML](https://toml.io) format.
Every key has a sensible default, so a missing or empty file is a valid
configuration. `relay setup` writes the keys that describe your setup, and
**Settings** in the app changes the rest. You can also edit the file by
hand. Restart the web server afterwards to apply your changes:

```bash
systemctl --user restart relay     # does not touch your terminals
```

## Where the file lives

| Situation | Path |
| --- | --- |
| Default | `~/.config/relay/relay.toml` (or `$XDG_CONFIG_HOME/relay/relay.toml`) |
| `RELAY_HOME` is set | `$RELAY_HOME/config/relay.toml` |
| `RELAY_CONFIG` is set | Exactly that path |

The file is created with mode `0600`, so only you can read it. It contains
your password hash.

:::note
When you change settings in the app, Relay rewrites `relay.toml`, and
comments in the file are not kept. Keep notes somewhere else.
:::

**Value formats.** Durations are strings such as `"90s"`, `"45m"`, `"12h"`
or `"30d"` (days). Paths may start with `~/`, which means your home folder.

## A complete example

```toml
[server]
listen = ":443"
domain = "relay.example.com"
tls = "auto"
acme_email = "you@example.com"
redirect_http = true

[auth]
user = "me"
# password_hash is written by `relay setup` / `relay passwd`; don't edit by hand.

[terminal]
record = "agents"
record_days = 14

[agents]
workspace_roots = ["~/code", "~/work"]

[previews]
mode = "auto"
ignore = [5432, 6379]

[notify]
ntfy_url = "https://ntfy.sh/relay-7c3e9a41f0b24d6e"
quiet_start = "22:00"
quiet_end = "07:00"

[[apps]]
id = "jupyter"
name = "Jupyter"
command = ["jupyter", "lab", "--no-browser", "--port", "8899", "--ServerApp.base_url=/apps/jupyter/"]
port = 8899
```

## `[server]`

How Relay listens, and the address people use to reach it. See
[Choose how to reach your machine](../getting-started/choose-access.md).

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `listen` | string | `"127.0.0.1:7777"` | Address and port for the web UI. Use `":443"` together with `domain` for automatic HTTPS |
| `domain` | string | `""` | Public hostname, for example `"relay.example.com"`. Required for automatic HTTPS and subdomain previews |
| `tls` | string | `"auto"` | `"auto"`: get a Let's Encrypt certificate when `domain` is set and `listen` ends in `:443`, otherwise plain HTTP. `"off"`: plain HTTP. `"manual"`: use `cert_file` and `key_file` |
| `cert_file` | string | `""` | Certificate chain (PEM) for `tls = "manual"` |
| `key_file` | string | `""` | Private key (PEM) for `tls = "manual"` |
| `acme_email` | string | `""` | Email address for Let's Encrypt notices |
| `public_url` | string | `""` | The address browsers use, when a proxy or tunnel handles HTTPS, for example `"https://relay-box.tail1234.ts.net"`. It sets the allowed origin and the passkey domain |
| `trusted_proxies` | list of CIDR | `[]` | Proxies whose `X-Forwarded-For` / `X-Forwarded-Proto` headers Relay believes, for example `["127.0.0.1/32", "::1/128"]` |
| `redirect_http` | bool | `false` | Also listen on port 80 and redirect to HTTPS (automatic HTTPS only) |

**How Relay works out its own address** (used for sign-in, passkeys and
links in notifications): `public_url` if set. Otherwise `https://<domain>`
with automatic or manual TLS. Otherwise the `listen` address.

## `[auth]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `user` | string | `"admin"` | Username for the account created from this file on first start |
| `password_hash` | string | `""` | argon2id hash written by `relay setup`. Relay imports it into its database on first start. Change your password with `relay passwd`, not here |
| `session_ttl` | duration | `"30d"` | How long a *Keep me signed in* session lasts without use |
| `short_ttl` | duration | `"12h"` | How long a normal session lasts without use |
| `insecure_cookies` | bool | `false` | Drop the `Secure` cookie flag. **Only** for plain-HTTP testing on a private network |

Passkeys, TOTP, devices and API tokens live in the database, not in this
file.

## `[terminal]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `shell` | string | `""` | Shell for new terminals. Empty means `$SHELL`, then `/bin/bash` |
| `default_cwd` | path | `"~"` | Folder new terminals start in |
| `scrollback_kb` | int | `2048` | Output kept per session for replay when you reconnect (KB) |
| `record` | string | `"agents"` | Which sessions to record: `"off"`, `"agents"` or `"all"` |
| `record_days` | int | `14` | Days to keep recordings |
| `upload_max_mb` | int | `512` | Largest file you can upload or paste (MB) |
| `import_tmux` | bool | `true` | List existing tmux sessions so you can attach to them |

## `[agents]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `workspace_roots` | list of paths | `["~/code", "~/src", "~/projects", "~/project", "~/work", "~/dev"]` | Folders scanned for git repositories (a few levels deep). Missing folders are skipped |
| `hooks` | bool | `true` | Offer to install attention hooks for supported agents |
| `index_history` | bool | `true` | Build the full-text search index over agent transcripts |
| `disabled` | list of strings | `[]` | Agent ids to ignore, for example `["aider", "crush"]` |
| `idle_seconds` | int | `8` | Seconds without output before an agent session counts as quiet (used by the "needs you" fallback) |

## `[files]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `root` | path | `"~"` | Files can browse only inside this folder. Links that point outside it are refused |
| `show_hidden` | bool | `false` | Show dotfiles by default |
| `use_trash` | bool | `true` | Delete to the Trash (restorable) instead of deleting permanently |

## `[previews]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `mode` | string | `"auto"` | `"auto"`, `"subdomain"`, `"path"` or `"off"`. See [Previews](../guides/previews.md#choose-subdomain-or-path-mode) |
| `host` | string | value of `server.domain` | Base name for subdomain previews: `<port>.<host>` |
| `port_min` | int | `1024` | Lowest port listed |
| `port_max` | int | `65535` | Highest port listed |
| `ignore` | list of ints | `[]` | Ports never listed, for example databases |

## `[desktop]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Offer the remote desktop (it also needs the desktop packages) |
| `display` | string | `":7"` | X display number used for the desktop |
| `geometry` | string | `"1600x1000"` | Starting resolution |
| `idle_stop` | duration | `"2h"` | Stop the desktop after this long with no viewer |

### `[[desktop.apps]]`

Extra apps for the desktop launcher. Repeat the table for each app.

| Key | Type | Meaning |
| --- | --- | --- |
| `id` | string | Short unique id |
| `name` | string | Name shown in the launcher |
| `command` | list of strings | Program and arguments, for example `["gimp"]` |
| `icon` | string | Optional icon name or path |

## `[code]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Offer Code (VS Code in the browser) |
| `binary` | string | `""` | Path to `code-server` or `openvscode-server`. Empty means auto-detect |
| `idle_stop` | duration | `"2h"` | Stop Code after this long without traffic |

## `[[apps]]`

Extra web apps served behind your login at `/apps/<id>/` and started on
demand. See [Code → Add other web apps](../guides/code.md#add-other-web-apps).

| Key | Type | Meaning |
| --- | --- | --- |
| `id` | string | Short unique id, used in the URL |
| `name` | string | Display name |
| `description` | string | Optional description |
| `command` | list of strings | Program and arguments to start the app |
| `port` | int | Loopback port the app listens on, **or** |
| `socket` | path | Unix socket the app listens on |
| `env` | table | Extra environment variables, for example `{ JUPYTER_TOKEN = "" }` |
| `cwd` | path | Folder to start in |
| `idle_stop` | duration | Stop the app after this long without traffic. Empty means never |

## `[notify]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `ntfy_url` | string | `""` | ntfy topic URL to forward notifications to |
| `webhook_url` | string | `""` | URL that receives every notification as JSON |
| `quiet_start` | string | `""` | Start of quiet hours, `"HH:MM"` in the machine's time zone |
| `quiet_end` | string | `""` | End of quiet hours |

Per-kind rules and push devices are stored in the database and managed in
**Settings → Notifications**.

## `[usage]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `claude_quota` | bool | `false` | Read your Claude plan usage using Claude Code's stored sign-in token. Opt-in. See [Agents → Usage and quotas](../guides/agents.md#usage-and-quotas) |
| `prices_url` | string | `""` | Optional URL of a model price table, to refresh the built-in prices |

## `[schedules]`

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Run [schedules](../guides/schedules.md). When `false`, no scheduled runs happen |

## Environment overrides

Some keys can be overridden with environment variables. These take
precedence over the file:

| Variable | Overrides |
| --- | --- |
| `RELAY_LISTEN` | `server.listen` |
| `RELAY_DOMAIN` | `server.domain` |
| `RELAY_PUBLIC_URL` | `server.public_url` |
| `RELAY_TLS` | `server.tls` |

See [Environment variables](environment.md) for the full list.

## Next steps

- [Environment variables](environment.md)
- [CLI reference](cli.md)
- [Choose how to reach your machine](../getting-started/choose-access.md)
