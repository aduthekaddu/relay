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
the `relay_webauthn` cookie, and each ceremony can be used once. The RP ID is
`server.domain`, or else the host of `cfg.Origin()`. Allowed origins are the
router's allowed origins. IP-address origins can't be RP IDs, so passkeys are
off there (`passkeysAvailable: false`).

**TOTP.** RFC 6238 (HMAC-SHA1, 30 s, 6 digits, ±1 step) is implemented in
`totp.go`. `totp/setup` returns a secret and an `otpauth://` URL, and
`totp/enable {code}` turns it on. The last used step is stored and claimed
atomically, so a code can't be used twice. Disabling needs a current code.

**Tokens.** `rly_…` tokens are sent as `Authorization: Bearer`, which gives
principal method `token`. Only the SHA-256 is stored, and the plaintext is
shown once. Manage tokens at `/api/v1/auth/tokens` or with `relay token
create|list|revoke`.

**Password reset.** `relay passwd` (`--stdin` for scripts, `--user` to name or
rename the account) writes the database directly, whether or not the server
is running, and signs out every browser session unless you pass
`--keep-sessions`.

## Sessions and cookies

- Session ids are 256-bit random values. Only their SHA-256 is stored in
  `auth_sessions`. Each row also has a separate public id, which the sessions
  list and `Principal.SessionID` show.
- Expiry slides: 12 h (`auth.short_ttl`), or 30 days with *remember*
  (`auth.session_ttl`). `last_seen`/expiry writes happen at most once a minute
  per session. Principals are cached for 5 s, so a revocation made from the
  CLI takes effect within 5 s. Revoking in the app takes effect at once and
  also closes that session's live WebSocket (`core.BusSessionRevoked`).
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
| `auth_user` | the single row: username, argon2id hash, WebAuthn user id, TOTP secret/pending/last step |
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

Open `http://localhost:47701`. Don't use `127.0.0.1`: an IP origin has no RP
ID, and the canonical origin must be `localhost` so that the browser's
`Origin` header is allowed. Chrome DevTools → More tools → WebAuthn → *Enable virtual authenticator
environment* gives a software authenticator with resident keys. Automated
tests (`internal/auth/passkey_test.go`) run a full register → sign-in round
trip with an in-test ES256 authenticator, plus option-shape and error-path
checks.

```sh
scripts/dev/safe go test ./internal/auth/... ./internal/live/... ./internal/info/... ./internal/server/...
```
