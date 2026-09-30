---
title: Previews
description: Open the dev servers running on your machine from any device over HTTPS, in subdomain or path mode, with a QR code for your phone.
---

When you or an agent start a dev server on the machine (Vite, Next.js,
Django, Rails, Storybook and so on), Relay notices it within a couple of
seconds. It offers a **preview**: a link that opens that server in any
browser, over HTTPS and behind your Relay login. Scan the QR code, and the
app you are building is on your phone, with no port forwarding or tunnel
tool needed.

## Open a preview

1. Start a dev server as usual, for example `pnpm dev` in a Relay terminal.
2. Relay shows a toast such as **Vite on :5173 · Open**, and sends a
   *preview* notification if you have turned those on.
3. Select **Open**, or go to **Previews** and select the port.

The **Previews** screen lists every listening port that belongs to your
user, with:

- the **process** and its folder, and the matching workspace,
- the **framework** and page **title**, when Relay can tell,
- whether it answers HTTP right now.

For each preview, you can **open** it, show a **QR code** to open it on
your phone, **copy** the link, give it a **label**, **pin** it to the top,
**hide** it, or **stop** the process.

Other ways to open a preview:

- Click a `http://localhost:5173` link in a Relay terminal. It opens the
  preview instead of your own computer's localhost.
- Run `relay preview 5173` in a Relay terminal. It prints the link and a QR
  code right in the terminal.
- Type `preview 5173` in the [command center](command-center.md).

:::note[Automatic detection is Linux-only]
On macOS, Relay cannot list listening ports yet. Open previews by port with
`relay preview <port>` or with `/p/<port>/` after your Relay address.
:::

## Choose subdomain or path mode

Relay can serve a preview in two ways:

| | Subdomain mode | Path mode |
| --- | --- | --- |
| Address | `https://5173.relay.example.com/` | `https://relay.example.com/p/5173/` |
| Needs | A wildcard DNS record (`*.relay.example.com`) | Nothing |
| Compatibility | Everything works: apps think they are at `/`, and WebSockets and hot reload work | Most apps work. Apps that use absolute paths (`/assets/…`) may need a *base path* setting |
| Isolation | A separate origin from Relay | A strict browser sandbox |

The default, `auto`, uses subdomain mode when wildcard DNS works for your
address, and path mode otherwise. Set it yourself with
[`previews.mode`](../reference/configuration.md#previews): `auto`,
`subdomain`, `path` or `off`.

| Access mode | What you get with `auto` |
| --- | --- |
| Your own domain, with a `*.` record | Subdomain mode |
| sslip.io | Subdomain mode (works automatically) |
| Tailscale | Path mode |
| Cloudflare Tunnel / your own proxy | Path mode, unless you route a wildcard hostname to Relay |

## Set up wildcard DNS for subdomain mode

1. At your DNS provider, add an **A record** with the name `*.relay` (for
   `relay.example.com`) pointing to the same IP address as `relay`. Add an
   `AAAA` record too if you use IPv6.
   [Step 2 of the domain guide](../getting-started/choose-access.md#step-2-create-the-dns-record)
   shows where to find these settings.
2. Wait a few minutes, then check that a random name resolves:

   ```bash
   dig +short 5173.relay.example.com
   ```

3. Restart Relay (`systemctl --user restart relay`) so that `auto` mode
   notices the change.

With automatic HTTPS, Relay gets a certificate for each preview address the
first time you open it. The first visit to a new port can therefore take a
few seconds.

To use a different base name for previews, set
[`previews.host`](../reference/configuration.md#previews), for example
`previews.host = "dev.example.com"` for `5173.dev.example.com`. The
wildcard record must match that name.

## Fix apps that break in path mode

In path mode, the app is served under `/p/5173/`, but many dev servers
assume they are at `/`. Relay rewrites redirects and cookie paths, but it
cannot change links inside your app. If a page loads without styles or
scripts:

- **Use subdomain mode** if you can. It is the most reliable fix.
- Or set the app's base path: Vite `base: '/p/5173/'`, Next.js `basePath`,
  Astro `base`, or the equivalent setting in your framework.

## Security

- Previews are behind your Relay login. Nobody without your password or
  passkey can open them.
- Your Relay session cookie is **never** sent to the dev server, and
  neither is any `Authorization` header. In subdomain mode, the preview gets
  its own short-lived cookie that only works for that one address.
- Path-mode pages run in a browser sandbox, so a page in a preview cannot
  act as Relay, even if it is malicious.
- Relay only forwards to ports on `127.0.0.1` of your own machine.

To hide ports you never want listed (a database, for example), add them to
[`previews.ignore`](../reference/configuration.md#previews). You can also
narrow the port range with `previews.port_min` and `previews.port_max`.

## Next steps

- [Desktop](desktop.md): when you need a real browser on the machine.
- [Choose how to reach your machine](../getting-started/choose-access.md)
- [Terminal](terminal.md): clickable `localhost` links.
