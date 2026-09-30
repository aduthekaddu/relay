---
title: Code
description: Open VS Code in your browser (code-server), behind your Relay login, started on demand and stopped when idle.
---

**Code** gives you VS Code in your browser, running on your machine. It uses
[code-server](https://github.com/coder/code-server) (or openvscode-server)
behind your Relay login. You don't need a second password, and no extra
port is opened. Relay starts it the first time you open it and stops it
after two idle hours, so it only uses memory while you need it.

## Install code-server

1. Open **Toolbox**, find **code-server** in the *editors* category, and
   select **Install**. You can also pick **Code** during `relay setup`.
2. Relay finds the program automatically. It looks at `code.binary` in your
   config, your `PATH`, `~/.local/bin` and `~/.local/lib/code-server-*`.

To use a specific binary, set [`code.binary`](../reference/configuration.md#code).

## Open a project

- From a **workspace** on Home or in the command center, choose **Open in
  Code**.
- From a folder in [Files](files.md), choose **Open in Code**.
- Or open **Code** in the navigation and pick a folder.

The first start takes a few seconds, and a "Starting…" page refreshes by
itself. After that, VS Code opens at `/apps/code/`, at the folder you
picked.

:::tip
Install Relay as an app (see [First steps](../getting-started/first-steps.md#add-relay-to-your-home-screen))
so that shortcuts like ⌘W and ⌘P go to VS Code instead of the browser.
:::

## Extensions and settings

Relay runs its own code-server with its own settings and extensions,
stored in Relay's data folder (`~/.local/share/relay/code/`). It is fully
separate from any other VS Code or code-server on the machine, so installing
an extension here changes nothing elsewhere. Telemetry and update checks
are turned off.

## Stop it

Code stops by itself after 2 hours without traffic
([`code.idle_stop`](../reference/configuration.md#code)). To stop it at once,
open **System → Services** or type `stop code` in the command center. Unsaved
editor changes are kept by VS Code's own hot exit.

To turn Code off entirely, set `code.enabled = false`.

## Add other web apps

The same mechanism can serve any web app that listens on a local port or
socket, started on demand behind your login at `/apps/<id>/`. For example,
Jupyter:

```toml
[[apps]]
id = "jupyter"
name = "Jupyter"
command = ["jupyter", "lab", "--no-browser", "--port", "8899",
           "--ServerApp.base_url=/apps/jupyter/"]
port = 8899
idle_stop = "1h"
```

Many apps need to know that they are served under `/apps/<id>/`, as with
`base_url` above. All options are in [`[[apps]]`](../reference/configuration.md#apps).

## Security

code-server runs with its own authentication turned off, but it listens only
on a private socket that belongs to your user. The only way in is through
Relay, after you sign in. Relay does not pass your session cookie on to
code-server.

## Next steps

- [Files](files.md)
- [Agents → Review what an agent changed](agents.md#review-what-an-agent-changed)
- [Toolbox](toolbox.md)
