---
title: Notifications
description: Get pushed when an agent needs you, a job finishes or someone signs in. Covers iPhone, Android, desktop, rules, quiet hours, ntfy and webhooks.
---

Relay can tap you on the shoulder. An agent waiting for an answer, a
scheduled run that finished, a new dev server or a sign-in from a new
device can each send a push notification to your phone and computers.
Every notification also lands in the in-app inbox (the bell), and you can
forward them to ntfy or any webhook. Relay does not push anything about a
terminal you are already looking at.

## Kinds of notifications

| Kind | Sent when | On by default |
| --- | --- | --- |
| **attention** | An agent or terminal needs you (hook, bell or prompt detected) | Yes |
| **done** | An agent finished its turn, or `relay notify --kind done` | Yes |
| **exited** | A terminal session's program exited | No |
| **schedule** | A scheduled run succeeded or failed | Yes |
| **security** | A sign-in from a new device or browser | Yes |
| **preview** | A new dev server started | No |
| **system** | Relay itself has news (for example, an update is available) | Yes |
| **custom** | Anything you send with `relay notify` | Yes |

Turn each kind on or off in **Settings → Notifications**.

## Turn on push on iPhone and iPad

On iOS and iPadOS, web push works only for sites added to the Home Screen,
and only on iOS/iPadOS **16.4 or newer**.

1. [Add Relay to your Home Screen](../getting-started/first-steps.md#iphone-and-ipad-safari)
   from Safari.
2. Open Relay **from the Home Screen icon**, not from Safari, and sign in.
3. Go to **Settings → Notifications** and select **Turn on notifications on
   this device**.
4. Tap **Allow** when iOS asks.
5. Select **Send a test notification**.

If the button is missing or greyed out, you are probably in Safari rather
than the Home Screen app. Check [Troubleshooting](troubleshooting.md#notifications-do-not-arrive).
iOS shows Relay's notifications under **Settings → Notifications → Relay**,
where you can pick banners, sounds and lock-screen display.

## Turn on push on Android

1. Open Relay in Chrome (installing it as an app is recommended, but not
   required).
2. Go to **Settings → Notifications** and select **Turn on notifications on
   this device**.
3. Tap **Allow**, then send a test notification.

If notifications don't arrive while the phone is idle, battery optimisation
may be holding them back. Allow Chrome (or the Relay app) to run
unrestricted in Android's battery settings.

## Turn on push on a computer

In Chrome, Edge, Firefox or Safari, open **Settings → Notifications**, select
**Turn on notifications on this device**, and allow them. On macOS, also
check **System Settings → Notifications** for your browser. The browser
must be running for notifications to arrive. An installed Relay app does
not need to be open.

## Choose what reaches you

In **Settings → Notifications**:

- **Rules:** turn each kind on or off.
- **Quiet hours:** for example 22:00 to 07:00. During quiet hours, Relay
  stores notifications in the inbox but does not push them. **Attention**
  and **security** notifications still come through, because they are the
  ones you can't miss.
- **Devices:** how many devices receive push. To stop one device, turn
  notifications off on that device, or revoke its session under
  [Security → Devices](security.md#see-and-sign-out-devices).

Relay also avoids noise by itself:

- If you are **looking at** a terminal, notifications about it are not
  pushed. They still go to the inbox.
- Repeats of the same kind for the same session within 30 seconds are
  merged.

## Forward to ntfy

[ntfy](https://ntfy.sh) is a simple push service with apps for Android and
iOS, and you can host it yourself. It is useful as a second channel, or on
devices where web push is awkward.

1. Install the ntfy app and subscribe to a topic with a long, hard-to-guess
   name. Anyone who knows the topic name can read it. For example:
   `relay-7c3e9a41f0b24d6e`.
2. In **Settings → Notifications → ntfy**, enter the topic URL, for example
   `https://ntfy.sh/relay-7c3e9a41f0b24d6e`, or the URL on your own ntfy
   server.
3. Send a test notification.

Relay sends the title, the message, a priority (high for attention), tags
and a click link that opens the right screen in Relay.

## Send to a webhook

Relay can POST every notification as JSON to a URL of your choice. Chat
tools, home-automation hubs and email gateways can use it.

In **Settings → Notifications → Webhook**, enter the URL. Relay sends:

```json
{
  "title": "codex needs you",
  "body": "Approve running `pnpm test`?",
  "kind": "attention",
  "link": "https://relay.example.com/terminal/t_abc123defg",
  "sessionId": "t_abc123defg",
  "at": "2026-10-01T02:14:07Z"
}
```

Each request times out after 5 seconds, and failures are not retried. For
services that expect a different format (for example Slack or Discord),
put a small relay in between, such as an automation service or a
serverless function.

The ntfy and webhook URLs are stored in `relay.toml` as
[`notify.ntfy_url` and `notify.webhook_url`](../reference/configuration.md#notify).

## Send your own notifications

From any Relay terminal:

```bash
relay notify "Backup finished"
relay notify -t "Deploy" --kind done "v2.3 is live"
long-job; relay notify --kind attention "long-job exited with $?"
printf 'line one\nline two\n' | relay notify -t "From stdin"
```

From anywhere else (another machine, CI), use an
[API token](security.md#create-api-tokens):

```bash
curl -fsS -X POST https://relay.example.com/api/v1/notify \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"CI","body":"main is green","kind":"done"}'
```

## Next steps

- [Agents → Get told when an agent needs you](agents.md#get-told-when-an-agent-needs-you)
- [Schedules](schedules.md)
- [Troubleshooting notifications](troubleshooting.md#notifications-do-not-arrive)
