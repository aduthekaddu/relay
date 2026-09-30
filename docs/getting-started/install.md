---
title: Install Relay
description: Install Relay with one command and answer a few setup questions. Covers flags, unattended installs, updates and uninstalling.
---

You install Relay by running one command on the machine you want to reach.
The command downloads Relay and checks that the download is genuine. It then
starts a short setup that asks how you will reach the machine, creates your
account and starts Relay. The whole process takes about two minutes.

:::tip[Before you start]
Decide how you will reach the machine. [Choose how to reach your machine](choose-access.md)
explains the options. If you are not sure, pick **Tailscale** for a private
setup or **sslip.io** to try Relay quickly on a VPS.
:::

## Run the installer

1. Open a terminal **on the machine**. On a VPS, connect with SSH first (for
   example `ssh you@203.0.113.10`). The [VPS guide](vps-guide.md) shows how.
2. Paste this command and press Enter:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
   ```

3. The installer detects your system and prints what it is about to do:

   ```text
   Relay installer
   → system: linux/amd64
   → downloading relay v0.1.0
   ✓ checksum verified (sha256)
   ✓ installed ~/.local/bin/relay
   → starting setup…
   ```

   Relay installs into your home folder, so the installer itself does not
   need `sudo`. Setup asks before any step that does. If `~/.local/bin` is not
   on your `PATH`, the installer prints the line to add to your shell profile.

:::note[What "checksum verified" means]
Every release publishes a list of SHA-256 fingerprints. The installer
computes the fingerprint of the file it downloaded and stops if it does not
match. This protects you from a corrupted or tampered download.
:::

## Answer the setup questions

Setup runs right after the installer. You can choose with the arrow keys and
Enter, or type the number of an option. Press Enter to accept the value shown
in brackets.

### 1. Choose how you will reach Relay

<!-- screenshot: docs/assets/screenshots/setup-access.png: the access-mode step of relay setup -->

```text
How will you reach this machine?
  1) Tailscale         private: only your devices can connect (recommended)
  2) My domain         https://relay.example.com with automatic HTTPS
  3) Instant address   https://<your-ip>.sslip.io, no domain needed
  4) My own proxy      Cloudflare Tunnel, Caddy, nginx… (Relay stays on 127.0.0.1)
  5) This machine only http://127.0.0.1:7777
```

What happens next depends on your choice:

- **Tailscale:** setup checks that Tailscale is installed and offers to run
  `tailscale serve --bg --https=443 http://127.0.0.1:7777`, which publishes
  Relay to your tailnet over HTTPS.
- **My domain:** setup asks for the domain and an email address for the
  certificate. It then checks that the domain's DNS points at this machine.
  If it does not, setup shows the exact record to create. See
  [Use your own domain](choose-access.md#use-your-own-domain-with-automatic-https).
- **Instant address:** setup asks before it looks up your public IP
  address, then builds an address like `https://203-0-113-10.sslip.io`.
- **My own proxy:** Relay listens only on `127.0.0.1:7777`. Setup asks for
  the public URL your proxy uses, so that sign-in and passkeys work.
- **This machine only:** Relay listens on `127.0.0.1:7777` and nothing else
  can reach it. This is useful on a laptop.

For **My domain** and **Instant address**, Relay listens on port 443. On
Linux, a normal user cannot use ports below 1024, so setup offers to run:

```bash
sudo setcap cap_net_bind_service=+ep ~/.local/bin/relay
```

This command lets the Relay binary use port 443 without running as root.
Setup explains the command and asks before running it.

### 2. Create your account

<!-- screenshot: docs/assets/screenshots/setup-account.png: username and password step -->

Pick a username, then either type a password twice or let setup generate a
strong one. A generated password is shown **once**, so save it in your
password manager.

Relay stores only an argon2id hash of your password, never the password
itself. You can change it later with [`relay passwd`](../reference/cli.md#relay-passwd)
or in Settings → Security.

### 3. Pick optional components

<!-- screenshot: docs/assets/screenshots/setup-components.png: optional components checklist -->

```text
Install optional components? (space to toggle, Enter to continue)
  [x] Agent CLIs     Claude Code, Codex, Gemini CLI…
  [x] CLI tools      ripgrep, fd, fzf, jq, gh, lazygit…
  [ ] Code           VS Code in the browser (code-server)
  [ ] Desktop        remote desktop with Chrome (about 400 MB)
```

You can skip all of these and install any of them later from the
[Toolbox](../guides/toolbox.md). Some need `sudo` (for example the desktop
packages). Setup tells you which ones before it runs them.

### 4. Start Relay

On Linux, setup installs two **systemd user services**: `relay-ptyd` (your
terminals) and `relay` (the web server). It turns on *lingering* with
`loginctl enable-linger`, which keeps your services running after you log
out and starts them at boot. On macOS it installs launchd agents instead.

### 5. Open it

<!-- screenshot: docs/assets/screenshots/setup-done.png: final screen with URL and QR code -->

Setup finishes with your address and a QR code:

```text
✓ Relay is running

  https://203-0-113-10.sslip.io

  ▄▄▄▄▄▄▄ ▄ ▄▄ ▄▄▄▄▄▄▄
  █ ▄▄▄ █ ▀█▄█ █ ▄▄▄ █     Scan to open on your phone,
  █ ███ █ ▄▀▀▄ █ ███ █     then "Add to Home Screen".
  …

  Check the setup any time with: relay doctor
```

Scan the code with your phone's camera and sign in with the account you
just created. Then continue with [First steps](first-steps.md).

## Install without questions

Every question has a flag, so you can script the install. Pass setup flags
after `bash -s --`:

```bash
curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh \
  | bash -s -- --yes --access domain --domain relay.example.com \
      --email you@example.com --user me --components agents,tools
```

To avoid putting a password in your shell history, pipe it in with
`--password-stdin`. If you leave the password out, setup generates one and
prints it. All setup flags are listed under
[`relay setup`](../reference/cli.md#relay-setup).

Installer options:

| Option | What it does |
| --- | --- |
| `--version v0.1.0` | Install a specific release instead of the latest |
| `--no-setup` | Only install the binary. Run `relay setup` yourself later |
| `--yes` | Accept every default and do not prompt |
| `--from-source` | Clone the repository and build it (needs Go, Node.js and pnpm) |
| `RELAY_INSTALL_DIR=/path` | Install the binary somewhere other than `~/.local/bin` |

## Check the installation

Run the built-in health check:

```bash
relay doctor
```

Every line should show ✓. A ⚠ or ✗ line comes with a hint that tells you
how to fix it. [Troubleshooting](../guides/troubleshooting.md#read-relay-doctor-output)
explains each check.

## Update Relay

```bash
relay update           # download, verify and install the latest release
relay update --check   # only tell me whether an update exists
```

`relay update` restarts the web server but **not** your terminals. Your
shells and agents keep running. Running the install command again also
upgrades Relay and keeps your settings.

## Uninstall Relay

```bash
relay uninstall          # stop and remove the services; keep your data
relay uninstall --purge  # also delete Relay's config and data (asks first)
```

Relay never deletes your own files, your projects or your agents' history.
`--purge` removes only Relay's own folders, which are listed in
[Backup and migrate](../guides/backup-and-migrate.md#what-relay-stores-and-where).

## Next steps

- [First steps](first-steps.md): sign in, install the app on your phone,
  and add a passkey.
- [Choose how to reach your machine](choose-access.md) if you want to change
  the access mode later.
