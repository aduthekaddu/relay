---
title: Choose how to reach your machine
description: Compare Tailscale, your own domain with automatic HTTPS, an instant sslip.io address, Cloudflare Tunnel and your own reverse proxy, with exact steps and DNS instructions.
---

Your browser needs a way to reach Relay, and that way should use HTTPS.
HTTPS encrypts everything between your phone and your machine, and passkeys
and push notifications only work over HTTPS. Relay supports five ways to get
there. This page compares them and gives exact steps for each, including
the DNS settings if you use a domain. You pick one during
[setup](install.md), and you can switch later.

## Compare the options

| | Tailscale | Your domain | sslip.io | Cloudflare Tunnel | Your own proxy |
| --- | --- | --- | --- | --- | --- |
| Who can reach the sign-in page | Only your devices | Anyone on the internet | Anyone on the internet | Anyone (or only you, with Cloudflare Access) | Depends on your proxy |
| Needs a domain | No | Yes | No | Yes (on Cloudflare) | Yes |
| Needs a public IP and open port 443 | No | Yes | Yes | No | Depends |
| App on each phone or laptop | Tailscale app | Nothing | Nothing | Nothing | Nothing |
| Passkeys and push | ✓ | ✓ | ✓ | ✓ | ✓ |
| Previews on their own subdomain | No (path mode) | ✓ with a wildcard DNS record | ✓ automatically | With extra setup | ✓ with a wildcard |
| Who holds the TLS certificate | Tailscale (on your machine) | Relay | Relay | Cloudflare | Your proxy |
| Setup time | 5 minutes | 10 minutes plus DNS wait | 2 minutes | 15 minutes | Varies |

**Our recommendation:** use **Tailscale** if you can install its app on
your devices, because nobody else on the internet can even see the sign-in
page. Use **your own domain** if you want to open Relay from any browser
without installing anything. Use **sslip.io** to try Relay right now.

## Use Tailscale (recommended)

[Tailscale](https://tailscale.com) builds a private network (a *tailnet*)
between your own devices. Relay listens only on `127.0.0.1`, and Tailscale
publishes it to your tailnet with a real HTTPS certificate, at an address
like `https://relay-box.tail1234.ts.net`. Devices outside your tailnet
cannot connect at all. The free personal plan is enough.

Pros: nothing is exposed to the internet, it works behind home routers
without port forwarding, and the certificate is automatic. Cons: every
device needs the Tailscale app and must be signed in to it. Previews use
path mode (`/p/5173/`) because tailnet addresses have no wildcard subdomains.

1. On the machine, install Tailscale and connect it:

   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up
   ```

   Open the link it prints and sign in. On macOS, install the Tailscale app
   from the Mac App Store instead.
2. In the Tailscale admin console, open **DNS** and make sure **MagicDNS** and
   **HTTPS Certificates** are turned on.
3. Run `relay setup` and choose **Tailscale**. Setup detects your tailnet
   name and offers to run:

   ```bash
   tailscale serve --bg --https=443 http://127.0.0.1:7777
   ```

   `--bg` keeps it running across reboots.
4. Install Tailscale on your phone and laptop, and sign in with the same
   account.
5. Open `https://<machine-name>.<tailnet>.ts.net` on your phone. The first
   visit can take a few seconds while the certificate is issued.

The resulting configuration:

```toml
[server]
listen = "127.0.0.1:7777"
public_url = "https://relay-box.tail1234.ts.net"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
```

:::caution
Use `tailscale serve`, not `tailscale funnel`. Funnel publishes the page to
the whole internet, which gives up the main benefit of this mode.
:::

## Use your own domain with automatic HTTPS

Relay listens on port 443 and gets a free certificate from
[Let's Encrypt](https://letsencrypt.org) for a domain you own, for example
`relay.example.com`. It renews the certificate automatically.

Pros: works from any browser with nothing to install, and gives you the
nicest addresses, including previews on their own subdomain. Cons: the
sign-in page is on the public internet. Relay is built for that (rate
limits, passkeys, optional TOTP), but it is still more exposed than
Tailscale. The machine needs a public IP address.

### Step 1: find your machine's public IP address

On a VPS, the IP address is shown in your provider's dashboard. You can also
run this on the machine:

```bash
curl -4 https://api.ipify.org; echo
```

It prints something like `203.0.113.10`. If the machine also has an IPv6
address (`curl -6 https://api64.ipify.org`), note that too.

### Step 2: create the DNS record

DNS is the internet's address book. It turns `relay.example.com` into your
IP address. You add the entry wherever you manage your domain: at your
registrar (the company you bought the domain from) or at your DNS host (for
example Cloudflare).

1. Sign in to your registrar or DNS host and open the **DNS** settings for
   your domain. The page may be called *DNS records*, *Zone editor* or
   *Advanced DNS*.
2. Add a record:

   | Field | Value |
   | --- | --- |
   | Type | `A` |
   | Name / Host | `relay` (for `relay.example.com`). Use `@` to use the bare domain |
   | Value / Points to | your IPv4 address, for example `203.0.113.10` |
   | TTL | Automatic (or 300 seconds) |

3. If your machine has IPv6, add a second record of type `AAAA` with the
   same name and your IPv6 address. If you do not know, skip it. **Do not**
   add an AAAA record that points somewhere else.
4. **For previews on subdomains** (optional but recommended), add one more
   record so that `5173.relay.example.com` also reaches your machine:

   | Type | Name / Host | Value |
   | --- | --- | --- |
   | `A` | `*.relay` | the same IPv4 address |

5. On Cloudflare, set the **Proxy status** of these records to **DNS
   only** (grey cloud). Relay handles HTTPS itself, and Cloudflare's proxy
   would get in the way. To use Cloudflare's network, use a
   [Cloudflare Tunnel](#use-cloudflare-tunnel) instead.

### Step 3: check that DNS works

A new record usually works within a few minutes, and sometimes it takes up
to an hour. Check from the machine:

```bash
dig +short relay.example.com
```

It should print your IP address. If `dig` is not installed, use
`nslookup relay.example.com` or a website such as dnschecker.org. `relay
setup` runs the same check and tells you what it found.

### Step 4: open port 443

Let the machine accept connections on port 443 (HTTPS). Optionally also
open port 80, so that `http://` links redirect to `https://`:

- In your **provider's firewall** (often called *Security groups* or
  *Firewall* in the dashboard), allow inbound TCP 443 (and 80).
- On the machine, if you use `ufw`:

  ```bash
  sudo ufw allow 443/tcp
  sudo ufw allow 80/tcp    # optional, for the redirect
  ```

### Step 5: run setup

Run `relay setup`, choose **My domain**, and enter `relay.example.com` and
your email address. Let's Encrypt emails you only if something is wrong
with your certificate. Setup offers the `setcap` command that lets Relay
use port 443 without root.

The resulting configuration:

```toml
[server]
listen = ":443"
domain = "relay.example.com"
tls = "auto"
acme_email = "you@example.com"
redirect_http = true
```

Open `https://relay.example.com`. Relay requests the certificate on the
first visit, which can take a few seconds.

## Use an instant sslip.io address

[sslip.io](https://sslip.io) is a free DNS service that turns any IP address
into a hostname: `203-0-113-10.sslip.io` resolves to `203.0.113.10`. Relay
gets a normal Let's Encrypt certificate for that name, so you get real HTTPS
without owning a domain.

Pros: works in two minutes, needs no domain and no DNS edits, and previews
on subdomains work automatically (`5173.203-0-113-10.sslip.io`). Cons: the
address is hard to remember and changes if your IP changes. It also depends
on a third-party DNS service, and Let's Encrypt limits how many certificates
can be issued for the shared sslip.io domain each week. It is great for
trying Relay. For a permanent setup, move to your own domain or Tailscale
later.

1. Open port 443 as in [Step 4](#step-4-open-port-443) above.
2. Run `relay setup` and choose **Instant address**. Setup asks for
   permission to look up your public IP (it asks `api.ipify.org`), then
   shows the address it will use.
3. Open the address. Bookmark it or add it to your home screen.

## Use Cloudflare Tunnel

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
runs a small program, `cloudflared`, on your machine. The program connects
out to Cloudflare, and Cloudflare serves `https://relay.example.com` and
forwards requests through the tunnel to Relay on `127.0.0.1`. You need a
domain that uses Cloudflare for DNS (the free plan is enough).

Pros: needs no public IP and no open ports, so it works behind home
routers and strict firewalls. You can add
[Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/policies/access/)
to require a second sign-in before anyone reaches Relay. Cons: Cloudflare
decrypts your traffic at its edge, so you are trusting Cloudflare with
it. Subdomain previews need extra setup.

1. Install `cloudflared` by following
   [Cloudflare's instructions](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)
   for your system.
2. Sign in and create the tunnel:

   ```bash
   cloudflared tunnel login                     # opens a browser; pick your domain
   cloudflared tunnel create relay              # prints the tunnel ID
   cloudflared tunnel route dns relay relay.example.com
   ```

3. Create `~/.cloudflared/config.yml`. Replace the tunnel ID and your
   username:

   ```yaml
   tunnel: relay
   credentials-file: /home/you/.cloudflared/<TUNNEL-ID>.json
   ingress:
     - hostname: relay.example.com
       service: http://127.0.0.1:7777
     - service: http_status:404
   ```

4. Start the tunnel. To try it, run it in the foreground; for everyday use,
   install it as a service:

   ```bash
   cloudflared tunnel run relay              # try it
   sudo cloudflared service install          # keep it running at boot
   ```

5. Run `relay setup`, choose **My own proxy**, and enter
   `https://relay.example.com` as the public URL.

The resulting configuration:

```toml
[server]
listen = "127.0.0.1:7777"
public_url = "https://relay.example.com"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
```

**Previews through a tunnel.** Path mode (`/p/5173/`) works out of the box.
For subdomain mode, add a wildcard hostname (`*.relay.example.com`) to the
tunnel's ingress and DNS, and set `previews.host = "relay.example.com"`.
Cloudflare's free certificate covers only one level of subdomain
(`*.example.com`), so a two-level name like `5173.relay.example.com` needs
an advanced certificate. The simpler alternative is to serve Relay from
the bare domain.

## Use your own reverse proxy

If you already run Caddy, nginx or Traefik, let it handle HTTPS and forward
to Relay on `127.0.0.1:7777`. The proxy must:

- pass WebSocket upgrades (terminals, live updates and the desktop use them),
- keep the original `Host` header and set `X-Forwarded-For` and
  `X-Forwarded-Proto`,
- allow long-lived connections (an hour or more) and request bodies of
  at least 100 MB (uploads are sent in chunks).

Then run `relay setup`, choose **My own proxy**, and enter the public URL.
The configuration is the same as for the tunnel above.

**Caddy.** Caddy gets certificates and handles WebSockets automatically:

```text
relay.example.com {
    reverse_proxy 127.0.0.1:7777
}
```

**nginx.** Use this server block (certificates from certbot or similar):

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl;
    http2 on;
    server_name relay.example.com;
    ssl_certificate     /etc/letsencrypt/live/relay.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relay.example.com/privkey.pem;

    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:7777;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }
}
```

:::caution
Only list your proxy's own address in `trusted_proxies`. Relay believes the
client IP in `X-Forwarded-For` only from those addresses, and it uses that IP
for sign-in rate limits and the device list. Trusting too much would let an
attacker fake their address.
:::

## Keep it on this machine only

Choose **This machine only** in setup to keep Relay on
`http://127.0.0.1:7777`. Nothing else can reach it. This is useful on a
laptop, or when you reach a server only through SSH. To use it from another
computer, forward the port over SSH:

```bash
ssh -L 7777:127.0.0.1:7777 you@203.0.113.10
```

Then open `http://localhost:7777` on that computer. Browsers treat
`localhost` as secure, so passkeys still work. Push notifications need one of
the HTTPS options above.

## Change how you reach Relay later

Run `relay setup` again and choose a different option. Setup offers to keep
your account and settings. You can also edit the `[server]` section of
`relay.toml` ([reference](../reference/configuration.md#server)) and
restart the web server:

```bash
systemctl --user restart relay
```

After you change the address, sign in again and add a passkey for the new
address, because passkeys are tied to the address they were created on.
If push notifications stop arriving, turn them on again on each device.

## Next steps

- [Install Relay](install.md)
- [Security](../guides/security.md): what each option exposes, and a
  hardening checklist.
- [Previews](../guides/previews.md): subdomain mode and path mode.
