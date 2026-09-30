---
title: Set up Relay on a VPS
description: A step-by-step guide from zero to a working Relay on a rented Ubuntu server, including SSH, the installer and pointing a domain.
---

This guide takes you from nothing to a working Relay on a VPS: a small
virtual server that you rent by the month from a hosting provider. You
create an Ubuntu server, connect to it with SSH, run the installer, and
optionally point a domain at it. No prior server experience is needed.
Budget about 20 minutes.

## Pick a provider and a size

Any provider that offers Ubuntu virtual machines works. Popular choices
include Hetzner, DigitalOcean, Linode (Akamai), Vultr, OVHcloud, Scaleway,
AWS Lightsail and Oracle Cloud. We are not affiliated with any of them. Pick
one with a data centre near you, because a closer server makes the terminal
feel more responsive.

Choose:

| Setting | Pick |
| --- | --- |
| Image / OS | **Ubuntu 24.04 LTS** (Debian 12 also works) |
| Architecture | x86 (amd64) or Arm (arm64). Relay supports both |
| Size | 2 vCPU and 4 GB memory to run agents comfortably. 1 vCPU and 1–2 GB is enough to try Relay |
| Disk | 40 GB or more if you will clone projects and install toolchains |
| Location | The region closest to you |

## Create an SSH key

SSH is how you open a terminal on the server from your computer. An SSH
*key* is a safer replacement for a password. If you already have one
(`~/.ssh/id_ed25519.pub`), skip to the next section.

On **macOS or Linux**, open Terminal. On **Windows**, open PowerShell. Then
run:

```bash
ssh-keygen -t ed25519 -C "my laptop"
```

Press Enter to accept the default location, and choose a passphrase. Then
show your **public** key:

```bash
cat ~/.ssh/id_ed25519.pub
```

It is one line that starts with `ssh-ed25519`. Copy it. This part is safe
to share. Never share the file without `.pub`, because that is your private
key.

## Create the server

1. In your provider's dashboard, choose **Create server** (it may be called
   *Droplet*, *Instance* or *Linode*).
2. Pick the image, size and location from the table above.
3. Under **SSH keys**, select **Add SSH key** and paste the public key you
   copied.
4. Give the server a name, for example `relay-box`, and create it.
5. After a minute, the dashboard shows the server's **public IPv4
   address**, for example `203.0.113.10`. Note it down.

:::tip
If the provider offers a firewall in the dashboard, create one now that
allows inbound **TCP 22** (SSH) and **TCP 443** (HTTPS), plus TCP 80 if you
want the http→https redirect. If you plan to use Tailscale, you only need
port 22, and you can even close that once Tailscale is set up.
:::

## Connect with SSH

From your computer:

```bash
ssh root@203.0.113.10
```

On the first connection, SSH asks whether you trust the server's
fingerprint. Type `yes`. You now see a prompt like `root@relay-box:~#`.

Some providers create a normal user such as `ubuntu` instead of `root`. If
so, use `ssh ubuntu@203.0.113.10` and skip the next step.

## Create your own user

Relay runs as a normal user, not as root. Create one, give it `sudo`, and
copy your SSH key to it:

```bash
adduser me                      # choose a password when asked
usermod -aG sudo me
rsync --archive --chown=me:me ~/.ssh /home/me
```

Log out (`exit`) and connect as the new user:

```bash
ssh me@203.0.113.10
```

## Update the server

```bash
sudo apt update && sudo apt -y upgrade
```

If it says a restart is required, run `sudo reboot`, wait a minute, and
connect again.

## Run the installer

```bash
curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
```

When setup asks how you will reach the machine:

- To **try it right now**, choose **Instant address**. You get
  `https://203-0-113-10.sslip.io` with no domain needed.
- To keep it **private**, choose **Tailscale** and follow
  [Use Tailscale](choose-access.md#use-tailscale-recommended).
- If you **own a domain**, point it at the server first (next section),
  then choose **My domain**.

Setup finishes with your address and a QR code. Scan it with your phone,
sign in, and continue with [First steps](first-steps.md).

## Point a domain at the server

Do this if you want an address like `https://relay.example.com`:

1. At your domain registrar or DNS host, add an **A record** with the name
   `relay` and the value `203.0.113.10` (your server's IP).
2. For dev-server previews on subdomains, also add an A record for
   `*.relay` with the same IP.
3. Wait a few minutes, then check on the server:

   ```bash
   dig +short relay.example.com
   ```

   It should print your server's IP.
4. Run `relay setup` again, choose **My domain** and enter
   `relay.example.com`. Setup keeps your account.

[Use your own domain](choose-access.md#use-your-own-domain-with-automatic-https)
explains each field and what to do on Cloudflare.

## Secure the server

Relay protects its own sign-in page. A few extra steps protect the server
itself:

1. **Turn on the firewall** and allow only what you use:

   ```bash
   sudo ufw allow OpenSSH
   sudo ufw allow 443/tcp
   sudo ufw allow 80/tcp     # optional
   sudo ufw enable
   ```

2. **Turn off password logins for SSH.** Your key is enough. Edit
   `/etc/ssh/sshd_config` (for example with `sudo nano /etc/ssh/sshd_config`),
   set `PasswordAuthentication no` and `PermitRootLogin no`, then run
   `sudo systemctl restart ssh`. Keep your current SSH window open and test
   the login in a new window before you close it.
3. **Install security updates automatically:**

   ```bash
   sudo apt install -y unattended-upgrades
   sudo dpkg-reconfigure -plow unattended-upgrades
   ```

4. **Turn on your provider's backups or snapshots.** They are usually a
   small monthly extra. See [Backup and migrate](../guides/backup-and-migrate.md)
   for what Relay itself stores.

## Next steps

- [First steps](first-steps.md): the home-screen app, a passkey and
  notifications.
- [Toolbox](../guides/toolbox.md): install your coding agents with one tap.
- [Security](../guides/security.md): the full hardening checklist.
