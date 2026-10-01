---
title: HTTP API
description: An overview of Relay's HTTP and WebSocket API, how to authenticate with API tokens or the local socket, and curl examples for common tasks.
---

Everything the Relay app does goes through a JSON API under `/api/v1`, and
you can use the same API from scripts, CI jobs and other machines. This page
covers authentication, conventions and working examples. The complete list
of endpoints and payloads is in the developer reference,
[docs/dev/API.md](https://github.com/aduthekaddu/relay/blob/main/docs/dev/API.md).
The Go types are in
[`internal/api`](https://github.com/aduthekaddu/relay/tree/main/internal/api).

## Authenticate

There are three ways to call the API:

| Method | Use it for | How |
| --- | --- | --- |
| **API token** | Scripts, CI, other machines | `Authorization: Bearer rly_…` |
| **Local socket** | Scripts on the machine itself | `curl --unix-socket "$RELAY_SOCKET" http://relay/api/v1/…` |
| **Browser session** | The Relay app | A cookie set at sign-in. Unsafe requests must also send an allowed `Origin` |

### Create a token

1. Open **Settings → Security → API tokens → Create token**, or run
   `relay token create ci` on the machine.
2. Copy the token. It is shown only once.
3. Store it as a secret, for example in an environment variable:

   ```bash
   export RELAY_URL=https://relay.example.com
   export RELAY_TOKEN=rly_xxxxxxxxxxxxxxxxxxxxxxxx
   ```

A token has the same power as your account, including starting terminals.
Revoke it when you no longer need it. [Security → API tokens](../guides/security.md#create-api-tokens)
explains how.

### Use the local socket

On the machine, the control socket accepts requests from your own user
without a token. Inside Relay terminals, `RELAY_SOCKET` points to it.
Elsewhere, it is at `$XDG_RUNTIME_DIR/relay/relay.sock`:

```bash
curl -s --unix-socket "${RELAY_SOCKET:-$XDG_RUNTIME_DIR/relay/relay.sock}" \
  http://relay/api/v1/terminals
```

The host name in the URL (`relay`) is ignored. Any name works.

## Conventions

- **JSON** request and response bodies, with `camelCase` field names.
  Timestamps are RFC 3339 (`2026-10-01T02:14:07Z`).
- **Errors** use the matching HTTP status and this body:

  ```json
  { "error": { "code": "not_found", "message": "session not found" } }
  ```

  Validation errors may add `field`. Rate-limit errors (429) may add
  `retryIn` (seconds). Common codes: `bad_request` (400), `unauthorized`
  (401), `forbidden` (403), `not_found` (404), `conflict` (409, for example
  a file changed since you read it), `unavailable` (503) and `internal`
  (500).
- **Long-running actions** (such as `git push` or a reindex) return `202
  Accepted` right away and report progress through the event stream.
- **IDs:** agent session ids have the form `<agent>:<nativeId>`, so always
  URL-encode them in paths.
- **Limits:** JSON bodies up to 1 MiB. Some endpoints are rate limited.
- **Public endpoints** (no authentication): `GET /api/v1/health`,
  `GET /api/v1/auth/state`, and the sign-in endpoints. Everything else needs
  authentication.

## Endpoint groups

| Group | Base path | Examples |
| --- | --- | --- |
| Info and settings | `/api/v1/info`, `/api/v1/settings` | Version, enabled features, settings |
| Auth | `/api/v1/auth/*` | Sessions, passkeys, TOTP, tokens, activity |
| Live events | `/api/v1/events` (WebSocket) | Everything that changes, as it happens |
| Terminals | `/api/v1/terminals`, `/api/v1/uploads` | Create, input, snapshot, attach, recording, uploads |
| Agents | `/api/v1/agents/*` | Sessions, transcripts, search, launch, resume, usage, hooks |
| Workspaces and git | `/api/v1/workspaces/*` | Status, diff, stage, commit, push, worktrees |
| Files | `/api/v1/files/*` | List, read, write, move, Trash, zip, search |
| System | `/api/v1/system/*` | Metrics, processes, services, logs |
| Previews | `/api/v1/previews` | Detected dev servers |
| Apps and desktop | `/api/v1/apps`, `/api/v1/desktop/*` | Start, stop, clipboard |
| Notifications | `/api/v1/notifications`, `/api/v1/notify`, `/api/v1/push/*` | Inbox, send, push |
| Clipboard, snippets, notes | `/api/v1/clip`, `/api/v1/snippets`, `/api/v1/notes` | |
| Schedules | `/api/v1/schedules` | CRUD, run now, history |
| Command center | `/api/v1/search`, `/api/v1/scripts`, `/api/v1/ask` | Federated search, script commands, Quick AI |
| Toolbox | `/api/v1/toolbox/*` | Tools, installs, MCP servers |

## Capability state

Authenticated `GET /api/v1/info` reports `capabilities.previews`,
`capabilities.code` and `capabilities.desktop`. Preview configured and
effective modes are separate; URLs use the effective decision. Code and
desktop separate `enabled`, current `available` prerequisites and `state`
(disabled, unavailable, stopped, starting, running or failed). Installation
alone does not mean running. These queries never start optional services.

Managed entries in `GET /api/v1/apps` and `GET /api/v1/desktop` include their
capability snapshots. Existing `features.code` and `features.desktop` mean
enabled and available. The `capabilities.changed` event tells clients to
refresh Info. [The developer contract](../dev/CAPABILITIES.md) defines every
field, discovery order, legacy state mapping and update timing.

## Examples

All examples assume `RELAY_URL` and `RELAY_TOKEN` are set as shown above.
They use `jq` to format output, which is optional.

### Check that Relay is up

```bash
curl -fsS "$RELAY_URL/api/v1/health"
# {"ok":true,"version":"v0.1.0"}
```

### Send a notification

```bash
curl -fsS -X POST "$RELAY_URL/api/v1/notify" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"CI","body":"main is green","kind":"done"}'
```

### List terminals

```bash
curl -fsS "$RELAY_URL/api/v1/terminals" \
  -H "Authorization: Bearer $RELAY_TOKEN" | jq '.[] | {id, name, status}'
```

### Start a command in a new terminal and read its screen

```bash
id=$(curl -fsS -X POST "$RELAY_URL/api/v1/terminals" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"tests","cwd":"~/code/app","command":["make","test"],"kind":"task"}' | jq -r .id)

sleep 5
curl -fsS "$RELAY_URL/api/v1/terminals/$id/snapshot?lines=20" \
  -H "Authorization: Bearer $RELAY_TOKEN" | jq -r .text
```

### Type into a terminal

```bash
curl -fsS -X POST "$RELAY_URL/api/v1/terminals/$id/input" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data":"git status\r"}'
```

`\r` is the Enter key. Set `"paste": true` to send the text as a bracketed
paste.

### Launch an agent in a new worktree

```bash
curl -fsS -X POST "$RELAY_URL/api/v1/agents/launch" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "agent": "claude",
        "cwd": "~/code/app",
        "prompt": "Fix the flaky test in auth_test.go and explain the cause.",
        "worktree": { "branch": "fix-flaky-auth" }
      }' | jq '{id, name}'
```

### Search agent conversations

```bash
curl -fsS -G "$RELAY_URL/api/v1/agents/search" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  --data-urlencode "q=rate limit" --data-urlencode "limit=5" | jq
```

### Resume an agent session

Session ids contain a colon, so URL-encode them:

```bash
sid=$(jq -rn --arg s "codex:0199a1b2-c3d4" '$s|@uri')
curl -fsS -X POST "$RELAY_URL/api/v1/agents/sessions/$sid/resume" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  -H "Content-Type: application/json" -d '{}'
```

### Search the whole machine

```bash
curl -fsS -G "$RELAY_URL/api/v1/search" \
  -H "Authorization: Bearer $RELAY_TOKEN" \
  --data-urlencode "q=router.go" | jq
```

### Run a schedule now

```bash
curl -fsS -X POST "$RELAY_URL/api/v1/schedules/<schedule-id>/run" \
  -H "Authorization: Bearer $RELAY_TOKEN"
```

## WebSockets

Three endpoints are WebSockets:

- `GET /api/v1/events` streams every change (terminals, notifications,
  previews and so on) as JSON messages.
- `GET /api/v1/terminals/{id}/attach` carries a terminal session: binary
  frames for output and input, and JSON text frames for control messages.
- `GET /api/v1/desktop/ws` carries the remote desktop (RFB).

Authenticate with the `Authorization` header, as for HTTP. The message
formats are specified in
[docs/dev/API.md](https://github.com/aduthekaddu/relay/blob/main/docs/dev/API.md#terminal-websocket-protocol).

## Next steps

- [Security → API tokens](../guides/security.md#create-api-tokens)
- [CLI reference](cli.md): most CLI commands are thin wrappers around this API.
- [Developer API reference](https://github.com/aduthekaddu/relay/blob/main/docs/dev/API.md)
