---
title: Environment variables
description: The RELAY_* environment variables. Covers the ones that change where Relay keeps its files or how it listens, and the ones Relay sets inside its terminals.
---

A few environment variables change where Relay keeps its files and how it
listens. They override `relay.toml`. Relay also sets some variables inside
every terminal it starts, so programs (and the `relay` command) know they
are running in Relay. You don't need any of them for normal use. They are
for development, testing, containers and scripts.

## Variables you can set

| Variable | Default | Effect |
| --- | --- | --- |
| `RELAY_HOME` | unset | Put **everything** under one folder: `$RELAY_HOME/config`, `$RELAY_HOME/data`, `$RELAY_HOME/cache` and `$RELAY_HOME/run` (sockets). Useful for development, tests and running two instances side by side. `~` is expanded |
| `RELAY_CONFIG` | unset | Use this exact file as `relay.toml` |
| `RELAY_LISTEN` | from config | Overrides [`server.listen`](configuration.md#server), for example `127.0.0.1:47700`. `relay serve --listen` sets it |
| `RELAY_DOMAIN` | from config | Overrides `server.domain` |
| `RELAY_PUBLIC_URL` | from config | Overrides `server.public_url` |
| `RELAY_TLS` | from config | Overrides `server.tls` (`auto`, `off` or `manual`) |
| `RELAY_NO_PTYD` | unset | When `1`, `relay serve` does not start a session daemon by itself. Use it when you manage `relay ptyd` separately |
| `RELAY_DEV` | unset | When `1`, also allow the local development origins (`http://127.0.0.1:47780`–`47789`) used by the web dev server. **Development only** |

Without `RELAY_HOME`, Relay follows the XDG base directory conventions:

| Folder | Location |
| --- | --- |
| Config | `$XDG_CONFIG_HOME/relay` (default `~/.config/relay`) |
| Data | `$XDG_DATA_HOME/relay` (default `~/.local/share/relay`) |
| Cache | `$XDG_CACHE_HOME/relay` (default `~/.cache/relay`) |
| Runtime (sockets) | `$XDG_RUNTIME_DIR/relay`, or `/tmp/relay-<uid>` (on macOS `$TMPDIR/relay-<uid>`) when that is unset |

## Installer variables

| Variable | Effect |
| --- | --- |
| `RELAY_INSTALL_DIR` | Folder to install the `relay` binary into (default `~/.local/bin`) |

## Variables Relay sets inside terminals

Every session started by Relay's session daemon gets these, in addition to
your normal login environment:

| Variable | Value | Used for |
| --- | --- | --- |
| `RELAY_SESSION` | The session id, for example `t_k3j9x2m4pq` | `relay notify`, `relay hook` and others link back to this session |
| `RELAY_SOCKET` | Path to the control socket (`relay.sock`) | `relay` commands inside terminals talk to the server here |
| `TERM` | `xterm-256color` | Colour and key support |
| `COLORTERM` | `truecolor` | 24-bit colour |
| `TERM_PROGRAM` | `Relay` | Lets programs detect Relay |
| `TERM_PROGRAM_VERSION` | The Relay version | |
| `LANG` | `C.UTF-8` if not already set | UTF-8 output |
| `DISPLAY` | The desktop's display (for example `:7`) while the desktop runs | Graphical programs appear on the [remote desktop](../guides/desktop.md) |

A script can detect that it runs inside Relay like this:

```bash
if [ -n "${RELAY_SESSION:-}" ]; then
  relay notify "Finished in Relay session $RELAY_SESSION"
fi
```

## Next steps

- [Configuration reference](configuration.md)
- [CLI reference](cli.md)
