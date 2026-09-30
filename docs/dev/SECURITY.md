# Security model

Relay gives a browser a shell on your machine. Treat every change as
security-sensitive.

## Threat model

- **Attacker on the internet** reaching the login page: must not get in
  (strong hashing, rate limits, passkeys, optional TOTP, no user
  enumeration, no default credentials — first run forces account creation
  locally or through the one-time setup flow).
- **Malicious web page in the same browser** (CSRF, cross-site WebSocket
  hijacking): blocked by `SameSite=Lax` cookies + strict `Origin` checks
  on every unsafe request and every WebSocket upgrade.
- **Untrusted content served by Relay** (files you preview, dev servers
  you proxy, HTML/SVG uploaded by an agent): must never run script on the
  Relay origin. Raw file responses carry `Content-Security-Policy:
  sandbox` + `X-Content-Type-Options: nosniff`; previews run on a separate
  origin (subdomain mode) or in a CSP sandbox (path mode) and never
  receive the Relay session cookie.
- **Other local users** on the machine: sockets are 0600 inside a 0700
  runtime dir, the control socket also checks the peer uid, data files
  are 0600.
- **Leaked browser session or token**: server-side sessions are revocable,
  tokens hashed, audit log shows use.

## Rules for code

1. Every route is authenticated unless it is on the short public list in
   `API.md`. New public routes need a review note.
2. Never trust client paths: `filepath.Clean`, resolve symlinks, check the
   result is inside the allowed root (`files.Root`) before any operation.
   Refuse `..` escapes, device files, sockets.
3. Never build shell command strings from user input. Use `exec.Command`
   with an argv slice. Agent prompts are passed as single argv elements.
4. Cap sizes: JSON bodies (1 MiB), uploads (config), text reads (5 MiB),
   diffs (1 MiB), log lines, OSC payloads, clipboard entries (256 KiB).
5. Timeouts on every outbound call and every subprocess (context).
6. Secrets (password hash, session ids, token hashes, VAPID private key,
   TOTP secret) never appear in logs, API responses or errors.
7. Destructive actions (delete, discard, kill, revoke) are POST/DELETE,
   audited, and confirmed in the UI.
8. Proxy (previews, apps): strip `relay_session`/`__Host-relay_session`
   cookies and `Authorization` before forwarding; only proxy to loopback
   ports/sockets; set `X-Forwarded-*` correctly; limit hop-by-hop headers.
9. HTTP responses: security headers from `server.SecurityHeaders`; HSTS
   when served over HTTPS; `Cache-Control: no-store` on API JSON.
10. Rate-limit expensive or abusable endpoints (login, search, ask,
    uploads start).

## Reporting

Security issues: email the maintainer (see `SECURITY.md` in the repo root)
rather than opening a public issue.
