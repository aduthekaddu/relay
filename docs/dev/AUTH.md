# Auth, sessions and live events

Relay is single-user: one account, many devices. The code is in
`internal/auth` (sign-in and credentials), `internal/live` (the events
WebSocket and `core.Presence`), `internal/info` (health, info, settings) and
`internal/server` (routing, CSRF, request info).

## Flows

**First run.** `GET /api/v1/auth/state` returns `setupRequired: true` while
there is no account. `POST /api/v1/auth/setup` creates it and signs in. From
the machine itself (loopback, or the control socket) no code is needed. From
anywhere else the body must carry the one-time `code` that `relay serve` logs
and writes to `<data>/setup-code` (0600). The file is deleted once the account
exists. If `relay setup` wrote `auth.password_hash` to `relay.toml`, it is
imported (as `auth.user`, default `admin`) on the first start instead.

**Password.** `POST /api/v1/auth/login` `{username,password,totp?,remember}`.
Wrong username and wrong password give the same `401 "Wrong username or
password."`, with equal timing: a dummy argon2id hash is checked when the
user doesn't exist. At most 2 argon2 checks run at once. When TOTP is on and
the password is right but `totp` is missing, the response is
`{ok:false, needTotp:true}` and no session is created.

**Passkeys.** Sign-in is discoverable (no username): `passkey/begin` →
`navigator.credentials.get` → `passkey/finish`. To register (signed in):
`passkeys/begin {name}` → `navigator.credentials.create` → `passkeys/finish`.
The ceremony state is kept in memory for 5 minutes, keyed by a random id in
the `relay_webauthn` cookie, and each ceremony can be used once. The RP ID
comes from the canonical origin: `server.domain` is used only when the
canonical hostname equals it or is its subdomain; otherwise the canonical
hostname is used. IP addresses cannot be RP IDs. Browser auth state and
setup/login suggestions also check the accessed origin; passkey begin is
unavailable on IP aliases even when the canonical RP ID is `localhost`.
See [canonical origins and aliases](#canonical-origins-and-aliases).

**TOTP.** RFC 6238 (HMAC-SHA1, 30 s, 6 digits, ±1 step) is implemented in
`totp.go`. `totp/setup` returns a secret and an `otpauth://` URL, and
`totp/enable {code}` turns it on. The last used step is stored and claimed
atomically, so a code can't be used twice. Disabling needs a current code.

**Tokens.** `rly_…` tokens are sent as `Authorization: Bearer`, which gives
principal method `token`. Only the SHA-256 is stored, and the plaintext is
shown once. Manage tokens at `/api/v1/auth/tokens` or with `relay token
create|list|revoke`.

**TOTP recovery.** `relay passwd --reset-totp` is a machine-local, owner-only
database operation for an existing account. It atomically replaces the password,
clears active/pending TOTP and its replay step, increments the recovery generation,
deletes every browser session and records `totp.recover`. There is no HTTP route.
The CLI refuses `--keep-sessions`, foreign ownership, symlinks, hard links and
non-private data/database modes. It uses no control socket and works stopped or
running with the same recovery-aware binary. An older running server must be
stopped before recovery and restarted with the updated binary. SQL failures return fixed text to avoid credential disclosure. The
recovery generation fences stale password/TOTP writes and session creation.
API tokens and passkeys remain valid; existing login limiters are unchanged.
See the [owner procedure](../guides/troubleshooting.md#i-am-locked-out).

**Password reset.** `relay passwd` (`--stdin` for scripts, `--user` to name or
rename the account) writes the database directly, whether or not the server
is running, and signs out every browser session unless you pass
`--keep-sessions`.

## Sessions and cookies

- Session ids are 256-bit random values. Only their SHA-256 is stored in
  `auth_sessions`. Each row also has a separate public id, which the sessions
  list and `Principal.SessionID` show.
- Expiry slides: 12 h (`auth.short_ttl`), or 30 days with *remember*
  (`auth.session_ttl`). Ordinary HTTP requests update `last_seen` and expiry
  at most once a minute. Socket validation never slides expiry.
- Every HTTP authentication reads SQLite. There is no principal cache.
  After a revocation commits, a new request using that credential fails,
  including changes made by `relay token revoke` or `relay passwd`.
- The events, terminal attach, desktop RFB and log WebSockets use
  `server.AcceptSocket` and `SocketGuard`. Each guard subscribes before its
  first uncached credential check, then checks every second. A backend
  `core.BusSessionRevoked` notification prompts an earlier check.
  Notifications are hints, because the bus can drop them. Offline database
  changes and expiry need no notification or reconnect.
- An affected socket stops applying new input and closes within **3 s** of
  revocation commit or session expiry: up to 1 s until the next check, 1 s
  for validation, and 1 s to force the transport closed. Cooperative peers
  receive WebSocket status 1008 with the fixed reason `authentication ended`.
  Slow or unresponsive peers may see an abrupt transport close. Database
  errors and validation deadlines fail closed.
- Terminal frames, live client events and each streamed desktop input chunk
  pass an uncached check before they reach the owner. Data already validated
  before the revocation commits can be in flight. Revocation disconnects the
  affected bridge, without killing the durable PTY or shared desktop.
  The owner-only local control socket is exempt from browser/token revocation.
- Cookie name: `__Host-relay_session` when the request is HTTPS (directly or
  via a trusted proxy's `X-Forwarded-Proto`) or the canonical origin is
  HTTPS. It is always `HttpOnly; SameSite=Lax; Path=/`, plus `Secure` on
  HTTPS. On plain HTTP the name is `relay_session`, and sign-in is refused
  (`403 insecure_origin`) unless the host is loopback or
  `auth.insecure_cookies = true`.
- `relay_device` is a long-lived random browser id. The first sign-in from an
  unseen device or browser sends a `security` notification.
- CSRF: cookie principals need an allowed `Origin` (and no
  `Sec-Fetch-Site: cross-site`) on unsafe methods and on every WebSocket
  upgrade. Bearer tokens and the control socket are not ambient credentials,
  so they are exempt.

## Canonical origins and aliases

`cfg.Origin()` selects one canonical external address, in this order:
`server.public_url` (also `RELAY_PUBLIC_URL`), an HTTPS `server.domain` when
automatic/manual TLS is configured, or the HTTP/HTTPS listener address.
An unspecified IPv4 listen host (`:port` or `0.0.0.0:port`) becomes
`localhost`. Other listener addresses are not rewritten. For a wildcard
IPv6 listener, a manual TLS listener on a nondefault external port, or a
reverse proxy, set `server.public_url` to the address the browser uses.
Use an HTTP(S) origin with a host and optional port; a root trailing slash
in configuration is removed, but paths, credentials, queries, fragments,
wildcards, IPv6 zones and ambiguous IP spellings are invalid.

`App.Origins` validates the canonical origin and adds automatic aliases
only when it is HTTP and its hostname is exactly `localhost`, `127.0.0.1`
or `::1`. All three names are then allowed at the **same effective port**.
This is a fixed list: it does not resolve DNS, accept arbitrary `127/8`
addresses or `.localhost` names as aliases, or broaden an HTTPS hostname's
certificate identity. An explicitly configured other loopback address can
be canonical, but gains no automatic aliases. The port is part of the
origin; omitted HTTP port equals `80`, and omitted HTTPS port equals `443`.
Explicit default ports normalize away. Nondefault ports must match exactly.

| Canonical origin | Automatically allowed browser origins | Passkeys |
| --- | --- | --- |
| `http://localhost:47733` | `http://localhost:47733`, `http://127.0.0.1:47733`, `http://[::1]:47733` | Only the localhost address; RP ID `localhost` |
| `http://127.0.0.1:47733` or `http://[::1]:47733` | The same three origins at `47733` | Unavailable at every alias; canonical IP has no RP ID |
| `http://localhost` or `http://localhost:80` | HTTP localhost, `127.0.0.1` and `[::1]` at port 80 | Only localhost |
| `https://relay.example.test` or explicit `:443` | That normalized HTTPS origin | RP ID `relay.example.test`, or a matching configured parent domain |
| `https://relay.example.test:47733` | That HTTPS origin at `47733` | Same RP ID; allowed origin still includes the port |
| `https://localhost:47733` | That HTTPS localhost origin only | RP ID `localhost` |
| `https://192.0.2.1` | That HTTPS IP origin only | Unavailable: HTTPS does not make an IP a valid RP ID |
| `http://192.0.2.1` or `http://[2001:db8::1]` | None | Unavailable |

Changing names does not transfer cookies: each hostname has its own browser
cookie jar and needs its own sign-in. Aliases permit Origin checks; they do
not make a listener reachable over both address families. Bind/listen or
forward the relevant loopback address if IPv6 access is needed.

Internal wiring can explicitly add a validated extra origin with
`App.AllowOrigin`; it does not receive automatic aliases. Wildcards and
remote HTTP IP origins are refused even as extras. The existing
`RELAY_DEV=1` opt-in adds HTTP localhost/IPv4 Vite origins on ports
47780–47789; this is an explicit development exception, not general
different-port aliasing. With that flag off, a familiar hostname on another
port, an unrelated hostname, a scheme change, `null`, an origin list,
duplicate/empty Origin headers or a decorated URL is refused. Malformed
Fetch Metadata and `Sec-Fetch-Site: cross-site` are also refused. Ordinary
browser Origin headers must be serialized origins without even a root slash.

Cookie unsafe requests and WebSocket upgrades require Origin and return
403 when it is absent or invalid. Public setup/password/passkey sign-in
POSTs allow a script that sends **neither** Origin **nor** Fetch Metadata;
a present empty header is not absence. A request carrying either header
must pass the browser check, so missing Origin with Fetch Metadata is 403.
Bearer/local authenticated operations retain their Origin exemption.
The existing `auth.insecure_cookies` switch affects plain HTTP cookie
issuance only; it grants no browser origins and never enables passkeys.

### Reverse proxies and passkey origins

Forwarded headers never set the canonical origin or RP ID. Configure
`server.public_url` explicitly, and have the proxy preserve the external
Host or supply `X-Forwarded-Host`, plus `X-Forwarded-Proto`. The accessed
origin uses those headers only from `server.TrustedProxies`: configured
valid CIDRs/IPs, or loopback peers when public_url is set and no valid
proxy networks exist. An explicit valid proxy list replaces that loopback
default. The first comma-separated host/protocol value is client-facing;
the trusted proxy must overwrite/sanitize client-supplied forwarding
headers. The standard `Forwarded` header is not used. Without request-info
middleware, only Host and direct TLS count.

The resulting accessed origin must still be in the configured allowlist.
A trusted proxy cannot add an unrelated host or unexpected port through a
header. An untrusted peer's forwarded HTTPS/hostname cannot enable
passkeys. If the proxy rewrites Host to a backend IP without supplying a
trusted external host, passkey availability fails closed; fix the proxy
headers or preserve Host. Origin checks continue to compare the browser's
Origin to the configured list, not to forwarded headers.

WebAuthn additionally accepts only allowed origins whose non-IP hostname
equals the RP ID or is its subdomain, and which are HTTPS or HTTP
localhost/`.localhost`. A local HTTP alias does not change RP selection:
configure a canonical localhost origin to use local passkeys. Changing
the canonical hostname or matching parent `server.domain` can change the
RP ID; existing credentials do not automatically migrate to a new RP.

## Revocation ownership

| Mutation | Owner | Socket notification | Credentials that remain valid |
| --- | --- | --- | --- |
| Logout, revoke one session | auth HTTP handlers | `SessionIDs` for removed rows | Other sessions and API tokens |
| Revoke others, password change | auth HTTP handlers | `SessionIDs` for removed rows | Calling browser session and API tokens; token/local callers keep no browser session |
| Revoke API token | auth HTTP handler | `TokenIDs` for the removed row | Other tokens and browser sessions |
| `relay token revoke` | CLI `Accounts` writer | None; guards poll SQLite | Other tokens and browser sessions |
| Ordinary `relay passwd`, including account rename | CLI `Accounts` writer | None; guards poll SQLite | API tokens; `--keep-sessions` also preserves browser sessions |
| `relay passwd --reset-totp` | CLI atomic recovery writer | None; guards poll SQLite | API tokens and passkeys; no browser sessions |
| Session expiry, offline row deletion or credential replacement | SQLite state | None; guards poll SQLite | Credentials whose rows remain valid |
| Account row removed | SQLite state | None; guards poll SQLite | No browser or token principal |

Passkey removal and TOTP changes do not themselves revoke existing sessions.
Their sessions use the same cookie checks when explicitly revoked. Machine-local TOTP recovery always revokes browser sessions, including the
invoking device. API tokens have no expiry column.

This bound covers Relay's four authenticated API WebSocket endpoints. Raw
IDE/app and preview reverse proxies use separate upgrade and delegated-cookie
handling. They do not use `SocketGuard` and are outside this API socket bound.

## Rate limits

Password, passkey, setup and TOTP attempts each have a per-IP limiter: 5
failures per 5 minutes, then an exponential lockout from 1 minute up to
1 hour, plus a machine-wide cap of 100 failures per 10 minutes. A locked-out
request gets a `429` with `retryIn`. Every failure is written to the audit
log. Client IPs come from `httpx.ClientIP` and honour `X-Forwarded-For` only
from `server.trusted_proxies`. When that list is empty, loopback is trusted
if `server.public_url` is set.

## Tables (feature `auth`)

| table | contents |
| --- | --- |
| `auth_user` | the single row: username, argon2id hash, WebAuthn user id, TOTP secret/pending/last step, recovery generation |
| `auth_sessions` | public id, token hash, method, remember, IP, UA, device/browser/OS, created/last seen/expires |
| `auth_passkeys` | credential id, public key, sign count, AAGUID, transports, backup flags, name, created/last used |
| `auth_tokens` | id, name, prefix, SHA-256, created/last used |
| `auth_audit` | activity log (last 5000). Also records `core.BusAudit` events from other features |
| `auth_devices` | hashed device ids already seen (new-device notices) |

## Live events

`GET /api/v1/events` (WebSocket). The server sends `hello` (the same
`api.Info` as `/api/v1/info`), then one `terminal.updated` per terminal, then
every bus event. Exceptions: backend topics are never sent, and `metrics`
goes only to clients that sent `{type:"subscribe",topics:["metrics"]}`.
Clients send `visibility {visible,path}` and `ping` (answered with `pong`).
`core.Presence.Watching(id)` is true while a visible client's path is
`/terminal/<id>`. The server pings every 25 s. A client whose 256-frame queue
fills up is dropped and should reconnect.

## Testing passkeys locally

`http://localhost:<port>` is a secure context, so passkeys work without TLS:

```sh
RELAY_PUBLIC_URL=http://localhost:47701 PORT=47701 NAME=auth scripts/dev/run.sh
```

Open `http://localhost:47701` as a top-level page. The canonical origin must
be localhost for local passkeys. The same-port `127.0.0.1` and `[::1]`
aliases support password sign-in and unsafe requests, but report
`passkeysAvailable: false`; changing the URL to localhost does not enable
passkeys if the configured canonical origin is still an IP. Remote devices
need an HTTPS hostname with trusted TLS. A browser's secure-context status
does not waive the RP ID domain requirement; check `window.isSecureContext`
and browser WebAuthn support. See the [Secure Contexts specification](https://www.w3.org/TR/secure-contexts/)
and [WebAuthn RP ID definition](https://www.w3.org/TR/webauthn-3/#rp-id).
Chrome DevTools → More tools → WebAuthn → *Enable virtual authenticator
environment* gives a software authenticator with resident keys. Automated
tests (`internal/auth/passkey_test.go`) run a full register → sign-in round
trip with an in-test ES256 authenticator, plus option-shape and error-path
checks; `origins_test.go` covers the alias and proxy boundary. These synthetic
authenticator tests do not prove real synced passkeys, physical devices or
conditional autofill. A Chromium virtual authenticator can exercise browser
secure-context/RP enforcement separately; report that environment explicitly.
The opt-in maintained `TestLocalOriginBrowser` uses an installed Playwright
module and Chromium without downloads or production dependencies. Set
`RELAY_ORIGIN_BROWSER_MODULE` to its module entry point and
`RELAY_ORIGIN_BROWSER_CHROME` to the browser executable, then run:

```sh
scripts/dev/safe go test ./internal/auth -run '^TestLocalOriginBrowser$' -count=1 -v
```

Its page/account are synthetic; it checks browser login and cookies at all
three aliases, different-port rejection and virtual resident-key registration
and discoverable sign-in. It does not exercise the complete Relay UI.

```sh
scripts/dev/safe go test ./internal/auth/... ./internal/live/... ./internal/info/... ./internal/server/...
```
