---
title: Troubleshooting
description: How to read relay doctor, and fixes for common problems with access, certificates, sign-in, terminals, agents, notifications, previews, desktop and Code.
---

Most problems can be diagnosed with one command: `relay doctor`. This page
explains its output line by line, then lists common problems with their
fixes, grouped by area. If you are stuck, the logs (`relay logs -f`) usually
say why. When you ask for help, include the output of `relay doctor` in the
report.

## Read `relay doctor` output

Run it on the machine, over SSH or in a Relay terminal:

```bash
relay doctor
```

Example output (your values differ):

```text
Relay doctor · relay v0.1.0 linux/amd64

  ✓ binary        ~/.local/bin/relay v0.1.0
  ✓ config        ~/.config/relay/relay.toml parsed, mode 0600
  ✓ directories   data, cache and runtime folders are private (0700)
  ✓ ptyd          session daemon healthy · 6 sessions
  ✓ server        web server healthy on 127.0.0.1:7777
  ✓ listen        port 443 bound by relay
  ⚠ dns           relay.example.com → 198.51.100.7, but this machine is 203.0.113.10
                  fix: update the A record at your DNS provider (see Choose access → Step 2)
  ✓ tls           certificate for relay.example.com valid for 71 days
  ✓ systemd       relay.service and relay-ptyd.service enabled and running · linger on
  ✓ disk          38 GB free in ~/.local/share/relay
  ✓ tools         git 2.43, rg 14.1, tmux 3.4
  ⚠ desktop       Xvnc not found
                  fix: install "Desktop" from the Toolbox, or: sudo apt-get install tigervnc-standalone-server openbox tint2
  ✓ code          code-server 4.x found
  ✓ agents        claude, codex, gemini

1 problem, 2 warnings
```

✓ means fine, ⚠ means something works less well than it could, and ✗ means
something is broken. Every ⚠ or ✗ line has a `fix:` hint. `relay doctor
--json` prints the same checks as JSON, for scripts.

| Check | What it looks at | If it fails |
| --- | --- | --- |
| **binary** | The version and location of `relay` | Reinstall, or put `~/.local/bin` on your `PATH` |
| **config** | `relay.toml` can be read and parsed, and only you can read it | Fix the TOML error it names. Run `chmod 600 ~/.config/relay/relay.toml` |
| **directories** | Relay's folders exist and are private | Run `chmod 700` on the folder it names |
| **ptyd** | The session daemon answers on its socket | `systemctl --user start relay-ptyd`, then check `relay logs` |
| **server** | The web server answers on the control socket | `systemctl --user start relay`, then check `relay logs` |
| **listen** | The configured port is bound by Relay | Another program has the port, or Relay lacks permission for port 443. See [Relay cannot use port 443](#relay-cannot-use-port-443) |
| **dns** | Your domain points to this machine's public IP (only with a domain) | Update the A/AAAA record. See [Choose access → Step 2](../getting-started/choose-access.md#step-2-create-the-dns-record) |
| **tls** | A certificate can be, or has been, obtained | See [Certificate errors](#the-browser-shows-a-certificate-error) |
| **systemd** | Both services are enabled and running, and lingering is on (Linux) | Run `relay setup` again, or `loginctl enable-linger $USER` |
| **disk** | Free space for the database, recordings and uploads | Free up space. Lower `terminal.record_days` |
| **tools** | git, ripgrep, tmux and friends | Install them from the [Toolbox](toolbox.md). Only git is essential |
| **desktop** | Xvnc and the desktop packages | Install **Desktop** from the Toolbox. Only needed for the remote desktop |
| **code** | code-server or openvscode-server | Install **code-server** from the Toolbox. Only needed for Code |
| **agents** | Which agent CLIs are found | See [An agent is not detected](#an-agent-is-not-detected) |

Other useful commands:

```bash
relay status          # one-line summary: running, URL, version
relay logs -f         # follow the logs of both services (Ctrl+C to stop)
systemctl --user status relay relay-ptyd
```

## Reaching Relay

### The page does not load

1. On the machine, run `relay status`. If Relay is not running, run
   `systemctl --user start relay-ptyd relay` and check `relay logs`.
2. Check the address. With Tailscale, your phone must be connected to
   Tailscale. With a domain, `dig +short relay.example.com` must print the
   machine's IP.
3. Check the firewalls. Port 443 must be allowed both in your provider's
   dashboard and on the machine (`sudo ufw status`).
4. Test from the machine itself: `curl -sS https://relay.example.com/api/v1/health`
   should print `{"ok":true,…}`. If that works but your phone cannot reach
   the page, the problem is between them (DNS, firewall, Tailscale).

### The browser shows a certificate error

With automatic HTTPS, Relay asks Let's Encrypt for a certificate on the
first visit. This fails if:

- **DNS does not point here yet.** Wait for DNS to update, then reload.
- **Port 443 is blocked** from the internet. Let's Encrypt must reach
  port 443 to verify your domain.
- **Cloudflare's proxy is on** (orange cloud) for the record. Switch it to
  **DNS only**, or use a [Cloudflare Tunnel](../getting-started/choose-access.md#use-cloudflare-tunnel).
- **Let's Encrypt's weekly limit** was reached. This is more likely with
  sslip.io, which many people share. Wait, or switch to your own domain.

`relay logs` shows the exact error from Let's Encrypt.

### Relay cannot use port 443

On Linux, normal programs cannot use ports below 1024. Setup grants the
`relay` binary that one permission with `setcap`. **Replacing the binary
removes the permission**, which can happen after `relay update` or a
reinstall. Grant it again and restart:

```bash
sudo setcap cap_net_bind_service=+ep "$(command -v relay)"
systemctl --user restart relay
```

If another program (such as nginx or Apache) already uses port 443, either
stop it or put Relay behind it. See
[Use your own reverse proxy](../getting-started/choose-access.md#use-your-own-reverse-proxy).

### Behind my proxy, the terminal keeps saying "Reconnecting…"

Your proxy is not passing WebSockets, or it closes idle connections. Make
sure it forwards the `Upgrade` and `Connection` headers and has a long
read timeout (an hour or more). The
[nginx example](../getting-started/choose-access.md#use-your-own-reverse-proxy)
has the right settings. Also check that `server.public_url` matches the
address in your browser exactly. Relay refuses live connections from other
origins.

### Relay stops when I log out of SSH

Lingering is off, so systemd stops your user services when you log out. Turn
it on:

```bash
sudo loginctl enable-linger "$USER"
```

## Signing in

### "Too many attempts"

After several wrong passwords, Relay makes you wait before the next try, and
the sign-in page shows a countdown. Wait for it to finish. If you did not
cause the failures yourself, check **Settings → Security → Activity** once
you are in, and consider [private access](../getting-started/choose-access.md#use-tailscale-recommended).

### I forgot my password

On the machine, over SSH, set a new one:

```bash
relay passwd
```

This works whether or not Relay is running, and it signs out every browser
session. API tokens and passkeys remain valid. TOTP stays enabled.

### I am locked out

If you lost your authenticator, a registered passkey can still sign you in.
Another signed-in device keeps its existing session, but turning off TOTP
through the web app still requires a current code. A session alone does not
replace a lost authenticator. Use the machine-local recovery below with or
without another signed-in device.

1. Open a shell on the Relay machine, locally or over SSH, as the OS user
   that owns the Relay service and database. An administrator must switch to
   that user. A browser login or API token alone cannot invoke a recovery API.
2. Select the same state directory as the service. If the service uses
   `RELAY_HOME`, set that exact value in your shell. Otherwise, use its XDG
   data directory. Do not delete the database or copy credentials into commands.
3. Set a new password with hidden prompts:

   ```bash
   relay passwd --reset-totp
   ```

4. Sign in with the new password. Set up TOTP with your replacement
   authenticator. Review passkeys and API tokens and revoke any you no longer trust.

The command works with the server stopped or running on the same updated
Relay version as the CLI. If the server still runs an older binary, stop it
with its service manager first and restart it with the updated binary after
recovery. This prevents older sign-in code from recreating a session in flight.
The command writes directly to
SQLite, so an absent or stale control socket is harmless. It requires an
existing database owned by the current OS user, a `0700` data directory and
regular, singly linked `0600` database files. It refuses symlinks, wrong owners
and insecure permissions without changing them. If permission checks fail,
verify the service owner and selected directory before correcting permissions
as that owner. Do not run against a different account or create fresh state.

Successful recovery replaces the password, clears active and pending TOTP,
and revokes every browser session, including another device you are still
using. `--keep-sessions` is rejected with `--reset-totp`. API tokens and
registered passkeys remain valid. Old cookies fail on the next request and
Relay's authenticated API WebSockets close within three seconds while the
server is running. The [socket contract](../dev/AUTH.md#sessions-and-cookies)
states the endpoints and limits. Raw app and preview proxy connections are
outside that bound. Recovery leaves durable terminals running.

Recovery records a `totp.recover` activity entry without passwords or TOTP
secrets. A failed recovery leaves credentials and browser sessions unchanged.
It does not clear an existing login rate-limit wait. Wait for the countdown
before signing in. There is no remote TOTP reset endpoint or recovery-code flow.

### "Sign in with a passkey" does nothing or finds no passkey

- Passkeys need HTTPS, or `http://localhost`. They don't work over plain
  HTTP on another address.
- Passkeys belong to the address they were created on. If you changed the
  domain, sign in with your password and add a new passkey.
- On iPhone, the Home Screen app and Safari share iCloud Keychain passkeys,
  but you must sign in to each separately.

## Terminal

### Characters are doubled or missing when I type on my phone

Use the **compose bar** for typing text. It works with autocorrect and
predictive keyboards. Use **direct mode** only for programs that react to
single keys. If a keyboard app still misbehaves in direct mode, turn off
its predictive text for the browser, or switch to your phone's built-in
keyboard.

### A shortcut does the wrong thing

Check **Settings → Terminal → Shortcuts**. **Auto** picks Mac or
Windows/Linux from your device. If you use a Mac keyboard on another
system (or the reverse), choose the profile yourself, or choose **Off** to
pass every key through unchanged. Remember that some shortcuts belong to
the browser. [Installing Relay as an app](../getting-started/first-steps.md#add-relay-to-your-home-screen)
frees most of them.

### My sessions are gone after a reboot

Programs cannot survive a reboot, but Relay remembers the sessions. Open
**Terminal** and select **Restore** on an exited session to start the same
command in the same folder. To resume agent conversations, use **Resume**
under Agents → History.

### The screen looks garbled after reconnecting

Press Ctrl+L (or **^L** on the key bar) to redraw. For full-screen programs,
resizing the window or rotating the phone also forces a redraw.

## Agents

### An agent is not detected

Relay looks for agents on the `PATH` of your login environment and in common
install folders. If you installed an agent in an unusual place:

1. Make sure the folder is on your `PATH` in `~/.profile` (or
   `~/.zprofile` on macOS), not only in `~/.bashrc`.
2. Restart only the web server. This does not touch your terminals:

   ```bash
   systemctl --user restart relay
   ```

3. Check `relay doctor` (the **agents** line).

If the agent is listed in `agents.disabled` in `relay.toml`, remove it
from there.

### A session never shows "Needs you"

Install the attention hooks for that agent in **Settings → Agents**. Agents
without hooks rely on terminal watch, which needs the output to stop at a
recognisable prompt. Hooks only affect sessions **started after** you
install them.

### History is empty or search finds nothing

The first index runs in the background and can take a few minutes with a
lot of history. The Agents screen shows progress. If it still looks wrong,
select **Reindex** in **Settings → Agents**. Also check that
`agents.index_history` is `true`.

## Notifications do not arrive

1. Send a test from **Settings → Notifications**. If the test arrives, the
   rule for that kind may be off, quiet hours may be active, or you were
   looking at that terminal at the time (Relay does not push about the
   screen you are on).
2. **iPhone/iPad:** you must use the **Home Screen app** on iOS 16.4 or
   newer, and turn notifications on inside that app. Check **iOS Settings →
   Notifications → Relay**.
3. **Android:** allow notifications for Chrome or the Relay app, and turn
   off battery optimisation for it.
4. **Desktop:** the browser must be running. Check the operating system's
   notification settings for your browser, and Focus / Do Not Disturb.
5. Push needs HTTPS. It does not work on `http://localhost` from another
   device.
6. If you changed Relay's address, turn notifications on again on each
   device.

## Previews

### My dev server is not listed

- Relay lists only servers that belong to **your user**, within
  `previews.port_min`–`previews.port_max`, and not in `previews.ignore`.
- Relay forwards to `127.0.0.1`, so the server must listen on
  `localhost`/`127.0.0.1` or on all interfaces (`0.0.0.0`). A server bound to
  one specific other address cannot be previewed.
- On macOS, detection is not available yet. Use `relay preview <port>`.

### The preview is blank or unstyled

This is a path-mode issue: the app expects to live at `/`. Use subdomain
mode, or set your app's base path. See
[Fix apps that break in path mode](previews.md#fix-apps-that-break-in-path-mode).

## Desktop and Code

### The desktop does not start

Run `relay doctor` and check the **desktop** line. Install **Desktop** from
the Toolbox if Xvnc or the window manager is missing. Then look at
`relay logs` for the exact error. The desktop is not available on macOS.

### Code shows "Starting…" forever

Check the **code** line in `relay doctor`. The first start after an install
can take up to a minute on a small machine. If it never starts, `relay
logs` shows code-server's own error. Very low memory (under 1 GB free) is
a common cause.

## Still stuck?

Open an issue at
[github.com/aduthekaddu/relay/issues](https://github.com/aduthekaddu/relay/issues)
and include:

- the output of `relay doctor` (it contains no secrets, but check it for
  anything private, such as your domain),
- what you did, what you expected and what happened,
- relevant lines from `relay logs`.

For security problems, don't open an issue. Follow
[SECURITY.md](https://github.com/aduthekaddu/relay/blob/main/SECURITY.md)
instead.

## Next steps

- [FAQ](faq.md)
- [CLI reference](../reference/cli.md)
- [Configuration reference](../reference/configuration.md)
