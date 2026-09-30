# Security policy

Relay gives a browser a shell on your machine, so we treat security reports
as our top priority. Thank you for helping keep Relay users safe.

## Supported versions

Relay is young. Security fixes go into the latest release, and we publish
them as a new patch release as quickly as possible.

| Version | Supported |
| --- | --- |
| Latest release (`v0.1.x`) | ✓ |
| `main` branch | ✓ (fixes land here first) |
| Older releases | ✗: please update with `relay update` |

Once Relay reaches 1.0, the latest two minor versions will receive security
fixes.

## Report a vulnerability privately

**Please do not open a public issue, discussion or pull request for a
security problem.**

Use GitHub's private vulnerability reporting:

1. Go to the repository's **Security** tab →
   [**Report a vulnerability**](https://github.com/aduthekaddu/relay/security/advisories/new).
2. Describe the problem, the affected version (`relay version`), and steps to
   reproduce it. A proof of concept helps a lot.
3. Tell us how you would like to be credited, if at all.

If you cannot use GitHub, email the maintainer at the address on
[their GitHub profile](https://github.com/aduthekaddu), with "Relay
security" in the subject line.

What to expect:

| When | What happens |
| --- | --- |
| Within 3 working days | We acknowledge your report |
| Within 10 working days | We confirm the issue (or explain why we think it isn't one) and share a plan |
| When a fix is ready | We release it, publish a GitHub security advisory (with a CVE where appropriate), and credit you unless you prefer otherwise |

We ask that you give us a reasonable time to fix the issue before you
disclose it publicly (90 days by default, and we are happy to coordinate).
We won't take legal action against good-faith research that follows this
policy.

## Scope

In scope: the code in this repository, including:

- `relay serve`: authentication (passwords, passkeys, TOTP, sessions, API
  tokens), CSRF and `Origin` checks, WebSocket endpoints, the HTTP API,
  and the reverse proxies for previews, Code and apps,
- `relay ptyd` and the local control socket (for example, access by another
  local user),
- the web app (for example XSS, or content from files or previews running
  on the Relay origin),
- `relay setup`, `relay update` and `scripts/install.sh` (for example,
  missing checksum verification or unsafe file permissions),
- files Relay writes: `relay.toml`, `relay.db`, agent hook configs and
  service units.

Out of scope:

- vulnerabilities in third-party tools that Relay starts or installs
  (code-server, TigerVNC, agent CLIs, Chrome, MCP servers). Please report
  those upstream. Tell us too if Relay's way of running them makes the
  problem worse,
- attacks that need your Relay password, a valid session, an API token or
  a shell as your user. Those already grant full access by design,
- insecure configurations that the docs warn against, for example
  `insecure_cookies = true` on a public network, or overly broad
  `trusted_proxies`,
- denial of service by sheer traffic volume, and missing hardening that
  has no demonstrated impact,
- social engineering, and physical access to an unlocked device.

## What we consider a vulnerability

Examples of what we want to hear about:

- signing in, or reaching any non-public endpoint, without valid
  credentials,
- bypassing rate limits, TOTP or passkey checks, or session revocation,
- a web page on another origin causing actions in Relay (CSRF, cross-site
  WebSocket hijacking),
- a previewed file, dev server or agent-produced content running script with
  the Relay origin's privileges, or getting the Relay session cookie,
- path traversal or symlink escapes outside `files.root`,
- command injection through any input (prompts, file names, arguments to
  script commands, git refs),
- another local user on the machine reaching the control socket, the session
  daemon, the desktop or code-server,
- secrets (password hash, session ids, tokens, TOTP secret, VAPID private key)
  appearing in logs, API responses or error messages,
- tampering with a download or update that goes undetected.

The design and the rules for contributors are in
[docs/dev/SECURITY.md](docs/dev/SECURITY.md). The plain-language model for
users is in [docs/guides/security.md](docs/guides/security.md).
