# Previews

`internal/previews` finds the dev servers you run and serves each one
behind Relay's login, either under `/p/<port>/` or on its own origin
`https://<port>.<previews.host>`.

## Detection

Every 2 s the service reads `/proc/net/tcp` and `/proc/net/tcp6`, keeps
sockets in state `0A` (LISTEN) and maps socket inodes to processes via
`/proc/<pid>/fd` — only for processes owned by the Relay user. Each port
gets exe, cmdline, cwd and a workspace (`Workspaces.RootOf(cwd)`).

Excluded: Relay's own listeners, ports outside
`previews.port_min..port_max`, and `previews.ignore`.

New ports are probed over HTTP (1 s timeout, first 64 KB): status,
`<title>`, and a framework hint from headers, markup and the command line
(Vite `/@vite/client`, Next.js, Astro, SvelteKit, Nuxt, Remix, Django,
Rails, Flask, FastAPI, Storybook, Jupyter, ...). HTTP ports are re-probed
every 30 s. Ports that do not speak HTTP are re-probed with backoff
(4 s, doubling up to 5 min), so a booting server is found fast and
databases are not hammered.

Changes publish `previews.changed`. A port that was never seen before and
stays up for 3 s sends a `preview` notification ("Vite on :5173"), with a
10-minute cooldown per port. Labels, pin and hide are stored in SQLite
(`PATCH /api/v1/previews/{port}`). Search provider id: `previews`.

## Modes

`previews.mode`:

- `path`: `/p/<port>/...`. The prefix is stripped before proxying;
  `Location` headers and `Set-Cookie` paths are rewritten back under the
  prefix.
- `subdomain`: `https://<port>.<host>/...` via `Router.HostDispatch`. Each
  preview gets its own browser origin, so apps that assume they own `/`
  work unchanged. `AllowTLSHost` lets autocert issue `<port>.<host>` certs.
- `auto` (default): subdomain when a random `<n>.<host>` resolves to the
  same addresses as `<host>` (wildcard DNS), else path. Rechecked every
  10 minutes. `localhost` hosts always support subdomains.
- `off`: no detection loop, list or proxy.

Auto mode begins in path/pending for non-local DNS hosts. Both lookups share
a three-second deadline. Missing/invalid/IP hosts use path. The same normalized
host and serving port drive URLs, dispatch and [Info capabilities](CAPABILITIES.md).
Mode changes refresh existing URLs immediately and invalidate browser Info.
Explicit subdomain mode does not verify DNS or TLS.

## Proxy rules (SECURITY.md)

Shared with apps in `internal/previews/revproxy`:

- Dials only `127.0.0.1:<port>` (or `[::1]`), never a host from the request.
- Strips Relay cookies (`relay_session`, `relay_preview`, ...) and
  `Authorization` from the upstream request. `X-Forwarded-*` is set from
  scratch.
- Path mode adds `Content-Security-Policy: sandbox allow-scripts
  allow-forms allow-popups allow-modals allow-downloads`. The preview gets
  an opaque origin and can never act as the Relay origin.
- Drops `Clear-Site-Data`, `Service-Worker-Allowed` and
  `Strict-Transport-Security` from upstream responses.
- WebSockets (HMR) go through `httputil.ReverseProxy`'s upgrade support.
- Unauthenticated navigations are redirected to sign in; other requests
  get 401.

## Subdomain handshake

The session cookie is host-only on the Relay origin and is never sent to
preview origins. Instead:

1. Request to `https://<port>.<host>/x` without a valid `relay_preview`
   cookie redirects to `<origin>/_relay/preview-auth?port=<port>&next=/x`.
2. That endpoint (signed-in users only) issues an HMAC-SHA256 token: key
   in the store KV, 60 s TTL, single use, bound to port and to an opaque
   hash of the caller's session. It redirects to
   `https://<port>.<host>/_relay/preview-callback?token=...&next=/x`.
3. The callback verifies the token and sets a host-only `relay_preview`
   cookie (HttpOnly, Secure, SameSite=Lax, 12 h) bound to the same port
   and session. Then it redirects to `next`, which must be a local path.

Tokens are rejected on a bad signature, a wrong kind, a wrong port, expiry
or reuse. Scripts can use `Authorization: Bearer <api token>` instead.

## API and CLI

| Method | Path | |
| --- | --- | --- |
| GET | `/api/v1/previews` | `Preview[]` |
| PATCH | `/api/v1/previews/{port}` | `UpdatePreviewRequest` -> `Preview` |
| GET | `/api/v1/previews/{port}/link` | `PreviewLink` (new, `internal/api/previews.go`) |
| ANY | `/p/{port}/...` | path mode |
| GET | `/_relay/preview-auth` | handshake, Relay origin |
| GET | `https://{port}.{host}/_relay/preview-callback` | handshake, preview origin |

`relay preview <port>` calls `/link` on the local control socket and prints
the label, the URL and a QR code drawn with Unicode half blocks.
