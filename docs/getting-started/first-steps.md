---
title: First steps
description: Sign in, install Relay on your phone's home screen, add a passkey, turn on notifications, open a terminal and start your first agent.
---

This page walks you through your first ten minutes with Relay. You sign in,
install Relay as an app on your phone and add a passkey, so you never type
your password on a phone again. Then you turn on notifications, open a
terminal and start a coding agent. Relay also offers these steps as a short
onboarding after your first sign-in. You can skip any of them and come back
later.

## Sign in

1. Open the address that setup printed, for example
   `https://relay.example.com`.
2. Enter the username and password you chose during setup.
3. Tick **Keep me signed in** on your own devices. You then stay signed in
   for 30 days of inactivity. Without it, the session ends after 12 hours without use.
4. Select **Sign in**. You see **Home**: what needs you, what is running,
   your workspaces and your machine's vital signs.

:::note
If the page says **Create your account**, setup did not set a password (for
example, because you ran it with `--no-setup`). Choose a username and a
strong password there, or run `relay passwd` on the machine.
:::

If you type a wrong password several times, Relay makes you wait before the
next attempt, and the wait grows with each failure. This protects you from
password guessing. The sign-in page shows a countdown.

## Add Relay to your home screen

Relay is a *progressive web app* (PWA): a website that installs like an app,
with its own icon and a full screen, and without the browser toolbar in the
way. On iPhone, installing it is also **required for push notifications**.

### iPhone and iPad (Safari)

1. Open your Relay address in **Safari**. Other browsers on iOS cannot
   install web apps with notifications.
2. Tap the **Share** button (the square with an arrow pointing up).
3. Scroll down and tap **Add to Home Screen**, then **Add**.
4. Close Safari and open **Relay** from your Home Screen. Sign in again. The
   app has its own sign-in, separate from Safari.

### Android (Chrome)

1. Open your Relay address in Chrome.
2. Tap the **⋮** menu, then **Install app** (on some phones it says **Add to
   Home screen**).
3. Confirm with **Install**. Relay appears in your app drawer and on your
   home screen.

### Desktop (Chrome or Edge)

Select the install icon at the right end of the address bar, or choose
**⋮ → Cast, save and share → Install page as app**. Relay opens in its own
window. This also frees browser shortcuts such as ⌘W for the terminal.

## Set up a passkey

A passkey lets you sign in with your fingerprint, your face or your device
PIN, with no password to type. Passkeys cannot be phished, because your
device only uses a passkey on the exact site it was created for.

1. Go to **Settings → Security → Passkeys**.
2. Select **Add a passkey** and give it a name, such as "iPhone" or
   "MacBook".
3. Follow your device's prompt (Face ID, Touch ID, Windows Hello or your
   screen lock).
4. The passkey appears in the list. Next time, select **Sign in with a
   passkey** on the sign-in page. Many browsers also offer the passkey
   automatically when you tap the username field.

Add a passkey on each device you use, or use one that syncs through your
password manager (iCloud Keychain, Google Password Manager, 1Password and
others).

:::caution
Passkeys belong to your Relay **address**. If you later move Relay to a
different domain, your passkeys stop working there and you sign in with your
password again. Keep your password in a password manager.
:::

## Turn on notifications

Relay can tap you on the shoulder when an agent needs you, when a scheduled
run finishes, or when someone signs in from a new device.

1. On the device that should receive notifications (on iPhone, inside the
   Home Screen app), go to **Settings → Notifications**.
2. Select **Turn on notifications on this device** and allow them when the
   browser asks.
3. Select **Send a test notification**. It should arrive within a few
   seconds, even with the app closed.

Repeat this on every device you want notified. [Notifications](../guides/notifications.md)
covers the rules for each kind, quiet hours, ntfy and webhooks.

## Open a terminal

1. Select **Terminal** in the navigation (the `>_` icon). On a phone, it is
   in the bottom tab bar.
2. Select **New terminal**. A shell opens in your home folder.
3. Type `echo hello` and press Enter.
4. Close the browser tab and open Relay again. Your terminal is still there,
   with its scrollback, because it runs on the machine and not in your
   browser.

On a phone, a **key bar** sits above your keyboard with Esc, Tab, Ctrl, the
arrows and other keys that phone keyboards lack. Swipe along the key bar to
move the cursor. The [Terminal guide](../guides/terminal.md) covers every
gesture and shortcut.

## Start an agent

If no agent is installed yet, open **Toolbox**, pick one (for example Claude
Code or Codex) and select **Install**. The install runs in a terminal you
can watch. Then sign in to the agent the way its own docs describe. Usually
you run it once in a terminal and follow the link it prints.

To start an agent session:

1. Open **Agents** and select **New session**, or press ⌘K / Ctrl+K and type
   `new agent`.
2. Pick the **agent**.
3. Pick a **workspace**: a recent project, or browse to a folder.
4. Optionally type a first **prompt**. You can also tick **New git worktree**
   to let the agent work on its own branch without touching your checkout.
5. Select **Start**. The agent opens in a terminal, and the session appears
   on the Agents board as *Working*.

When the agent stops to ask you something, the session turns *Needs you*
(an orange blinking dot and those words). The session also shows up at the
top of Home, and your phone gets a notification. For the most reliable
detection, install the attention hooks for your agent under **Settings →
Agents**. [Agents](../guides/agents.md) explains how.

## Next steps

- [Terminal](../guides/terminal.md): the key bar, compose, gestures and
  shortcuts.
- [Agents](../guides/agents.md): history, search, resume and review.
- [Security](../guides/security.md): TOTP, devices, and a hardening
  checklist.
