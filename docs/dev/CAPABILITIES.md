# Feature capability snapshots

Authenticated `GET /api/v1/info` and live `hello` include `Info.capabilities`.
The preview and apps services own these snapshots. Info reads their core
interfaces; it does not detect their tools or choose preview modes.
Queries never start Code, VNC, a desktop session, or a display probe.

Types: `internal/api/capabilities.go` and `web/src/api/capabilities.ts`.

## Preview fields

| Field | Meaning |
| --- | --- |
| configuredMode | Normalized startup setting: auto, subdomain, path or off. |
| effectiveMode | Current decision used for URLs, host dispatch and TLS host policy: subdomain, path or off. |
| host | Normalized preview base host, without its port. This is configuration, not a DNS/TLS readiness claim. |
| port | Optional serving port: explicit preview host port, otherwise the canonical origin's port. Path URLs use the canonical origin. |
| detection | off, not-required, missing-host, invalid-host, ip-literal, explicit, localhost, pending, verified, timeout, lookup-failed or address-mismatch. owner-unavailable indicates missing wiring. |
| checkedAt | Optional RFC 3339 time of the last completed DNS decision. Static decisions omit it. |

Names are trimmed, lowercased and stripped of one trailing dot. IP literals
are canonicalized; IPv6 URL authorities retain brackets. Ports must be
1–65535. Invalid URL syntax, empty labels and invalid DNS labels are rejected.
Absent, invalid or IP hosts select path, even with explicit subdomain mode.
Localhost and its subdomains select subdomain immediately in auto mode.

Auto mode starts in path with pending detection for other DNS names.
One three-second deadline covers the base and random wildcard lookups.
Equivalent canonical address sets select subdomain; timeout, lookup failure,
NXDOMAIN or mismatch select path. Rechecks run every ten minutes. The last
decision stays effective while a lookup is pending; shutdown cancellation
does not replace it. Explicit subdomain mode skips DNS verification and
does not establish wildcard DNS or TLS readiness.

Mode changes rebuild preview URLs and publish `previews.changed` immediately.
Mode or detection changes also publish `capabilities.changed`.
`GET /api/v1/previews` lists URLs from this decision; `/previews/{port}/link`
returns URL and mode from one locked snapshot.

## Managed app fields

| Field | Meaning |
| --- | --- |
| enabled | Startup configuration permits the feature. |
| available | Current prerequisites for a future explicit start are present. Independent of enabled and lifecycle. |
| state | disabled, unavailable, stopped, starting, running or failed. |
| missing | Stable prerequisite identifiers; no executable paths or child output. |
| implementation | Active binary family while starting/running; otherwise the next candidate. Code uses code-server/openvscode-server; desktop uses Xvnc/Xtigervnc. |
| source | Optional discovery category: configured, path, local-bin or standalone. |

Disabled overrides lifecycle. Otherwise starting/running/failed follow the
owning process manager. An available idle feature is stopped; missing
prerequisites with no active/failing lifecycle are unavailable.
Before a child is spawned, starting reports the current candidate selection.
Removing an installation can produce available=false with state=running.
The owner does not stop a ready child because a prerequisite disappeared.

Code checks an explicitly configured path or name without silently falling
back to another installation. With no configured binary, it checks PATH,
then local-bin, then supported standalone code-server directories, choosing
the newest version. Only regular executable files qualify. The Code owner
resolves command and environment again for each explicit start.

Desktop checks Linux, Xvnc or Xtigervnc, openbox, a local display matching
`:0` through `:999`, socket path length at most 100 bytes, and supported
session path characters. Missing identifiers are linux, vnc, openbox,
display, runtime-path-length and runtime-path-characters. Missing Code
uses code-binary; absent owner wiring uses owner. Display occupation and
actual readiness are checked during explicit start. Optional panel,
clipboard and resize tools have separate behavior.
Desktop running requires both X and session readiness. Failed session
cleanup retains failed until explicit retry or stop.

Prerequisite checks are fresh on every query, including negative results,
and on each explicit launch. The apps idle loop checks for changes every
30 seconds; idle-stop work can delay a poll. Lifecycle callbacks and discovery
changes publish invalidations. Queries remain fresh during this interval.

## Existing API meanings

| Reader | Contract |
| --- | --- |
| Info.capabilities | All three owner snapshots. |
| Info.features.code / desktop | enabled AND available; never proof of running. |
| Info.features.previewsMode / previewsHost | Effective mode; host is present only in subdomain mode. |
| App.capability | Additive snapshot on managed Code/Desktop entries; absent on custom web apps. |
| App.installed | Code: available. Desktop: enabled AND available. Disabled Code stays absent from the legacy list. |
| App.state | Legacy error for failed and unavailable for disabled; other states unchanged. |
| DesktopState.capability | Same desktop snapshot as Info and the managed App. |
| DesktopState.state | Legacy stopped plus generic error for failed; unavailable for disabled. |

`capabilities.changed` carries `{feature:"previews"|"code"|"desktop"}` only.
Browsers reload Info after this event and accept live hello snapshots.
A newer request or hello supersedes an older pending HTTP response.
Queries refresh before the next discovery event; checkedAt in a cached
browser snapshot remains its last fetched value until a refresh/reconnect.

Feature enabled flags, binary selection, preview host/mode and desktop
display are immutable startup settings. Editing TOML or the service
environment takes effect on the next serve start. They are not writable
through the six-field runtime Settings API. No query restarts services.
