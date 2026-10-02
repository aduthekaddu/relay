---
title: Security
description: How Relay keeps your machine safe, in plain language. Covers passkeys, TOTP, devices, API tokens, the activity log and a hardening checklist.
---

Relay gives your browser a shell on your machine. Anyone who signs in to
Relay can do anything your user can do. This page explains in plain
language how Relay keeps other people out, and what you can do to make it
even safer. It ends with a checklist to go through once.

## How Relay protects you

**One door.** Relay listens on a single port. Everything behind it listens
only on private sockets that belong to your user, or on `127.0.0.1`: the
terminals, VS Code, the desktop and the local `relay` command. None of
them can be reached from outside except through Relay's sign-in.

**Strong sign-in.**

- There is no default password. You create your account during setup.
- Your password is stored as an **argon2id** hash, a format designed to be
  slow to crack. Relay never stores the password itself.
- **Passkeys** let you sign in with your fingerprint or face. They cannot
  be phished, because your device only uses a passkey on the exact site it
  was created for.
- **TOTP** (six-digit codes from an authenticator app) can be added as a
  second step after your password.
- **Rate limits:** after 5 failed attempts from one address within 5
  minutes, Relay makes you wait, and the wait doubles with each further
  failure. A global limit slows down attacks that come from many addresses.
- Error messages are always the same ("Wrong username or password"), so
  nobody can find out which usernames exist.

**Sessions you control.** Every signed-in browser has its own session,
which you can see and revoke. Sessions end after 12 hours without use, or after 30 days
of inactivity when you tick *Keep me signed in*. Changing your password
signs out every other device. A sign-in from a device Relay has never seen
sends a **security** notification to your other devices.

**Other websites can't use your session.** Relay's cookie cannot be read by
scripts, and it is only sent over HTTPS. Relay also checks that every
action and every live connection comes from Relay's own page, so a malicious
site you visit in another tab cannot act as you.

**Untrusted content is contained.** Files you preview, dev servers you open
and pages an agent downloaded are shown in a browser sandbox or on a
separate address. They never receive your Relay cookie, so even a malicious
page cannot take over your session.

**Nothing leaves your machine.** Relay has no account, no cloud service and
no telemetry. It only contacts the internet when you ask it to (for
example, to get a certificate, check for an update or send a push
notification) or when a feature you turned on needs it.

## Set up a passkey

1. Go to **Settings → Security → Passkeys** and select **Add a passkey**.
2. Name it after the device, for example "iPhone", and confirm with your
   fingerprint, face or screen lock.
3. Sign out and select **Sign in with a passkey** to try it.

Add a passkey on at least two devices, or use one that syncs through your
password manager, so that losing one phone does not lock you out. Rename or
remove passkeys in the same list.

Passkeys need HTTPS (or `localhost`) and belong to your Relay address. If
you change the address, add new passkeys.

## Turn on two-step codes (TOTP)

1. Install an authenticator app, such as your password manager, Google
   Authenticator, Microsoft Authenticator or Aegis.
2. Go to **Settings → Security → Two-step codes** and select **Set up**.
3. Scan the QR code with the app, or type the secret key into it.
4. Enter the six-digit code the app shows, to confirm.

From then on, signing in with your password also asks for a code. To turn
it off, enter a current code in the same place.

:::caution
Store the secret key or a backup of your authenticator somewhere safe. If
you lose your authenticator, a passkey can still sign you in. Disabling TOTP
still needs a current code, even on a signed-in device. Machine-local
`relay passwd --reset-totp` recovery replaces the password and signs out all
browsers. API tokens and passkeys stay valid. Enroll TOTP again afterward. See
[Troubleshooting → I am locked out](troubleshooting.md#i-am-locked-out).
:::

## See and sign out devices

**Settings → Security → Devices** lists every signed-in session: device,
browser, operating system, IP address, and when it was last used.

- Select **Sign out** next to a session to end it immediately.
- Select **Sign out all other devices** if you think someone else has
  access.
- Changing your password (**Settings → Security → Password**, or
  `relay passwd` on the machine) also signs out every other device.

## Create API tokens

API tokens let scripts and other machines use Relay's
[HTTP API](../reference/api.md) without your password. Examples are
sending a notification from CI, or starting a terminal from a shortcut.

1. Go to **Settings → Security → API tokens** and select **Create token**.
   You can also run `relay token create <name>` on the machine.
2. Give it a name that says where it will be used, such as "ci-notify".
3. Copy the token. It starts with `rly_` and is shown **only once**. Relay
   keeps only a hash of it.
4. Use it as `Authorization: Bearer rly_…`.

The list shows when each token was last used. **Revoke** a token as soon
as you no longer need it, or if it may have leaked.

:::caution
A token can do everything you can do in Relay, including running commands.
Treat it like a password: keep it in a secret store and never commit it to a
repository.
:::

## Check the activity log

**Settings → Security → Activity** records security-relevant events:
sign-ins and failed attempts, passkey and token changes, session
revocations, token use, and destructive actions such as deleting files,
killing processes, discarding git changes and installing agent hooks.
Check it if something looks wrong.

## Hardening checklist

Go through this once after installing:

- [ ] **Prefer private access.** Use [Tailscale](../getting-started/choose-access.md#use-tailscale-recommended),
      or put [Cloudflare Access](../getting-started/choose-access.md#use-cloudflare-tunnel)
      in front of Relay, so the sign-in page isn't public.
- [ ] **Use a strong, unique password** from a password manager. Setup can
      generate one.
- [ ] **Add passkeys** on two devices, then sign in with them rather than
      the password.
- [ ] **Turn on two-step codes** if the sign-in page is public.
- [ ] **Turn on security notifications** so that you hear about new sign-ins.
- [ ] **Review devices** now and then, and sign out anything you don't
      recognise.
- [ ] **Keep API tokens few and named,** and revoke unused ones.
- [ ] **Keep Relay updated:** `relay update --check`, or watch for the
      *system* notification.
- [ ] **Secure the machine itself:** use SSH keys only, turn on a firewall that
      allows just SSH and 443, and install automatic security updates. The
      [VPS guide](../getting-started/vps-guide.md#secure-the-server) shows how.
- [ ] **Limit Files** with [`files.root`](../reference/configuration.md#files)
      if you only need one folder.
- [ ] **Don't enable `insecure_cookies`** except for a short test on a
      private network.
- [ ] **Behind a proxy,** list only the proxy's own address in
      [`trusted_proxies`](../reference/configuration.md#server).
- [ ] **Back up** `relay.toml` and `relay.db` ([how](backup-and-migrate.md)).

## Report a security problem

Please report vulnerabilities privately. See
[SECURITY.md](https://github.com/aduthekaddu/relay/blob/main/SECURITY.md).

## Next steps

- [Choose how to reach your machine](../getting-started/choose-access.md)
- [Backup and migrate](backup-and-migrate.md)
- [HTTP API](../reference/api.md)
