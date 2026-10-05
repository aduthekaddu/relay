# Synthetic owner-specific mocks

This backend is loaded only through `VITE_MOCK=1`. HTTP responses carry
`X-Relay-Mock: synthetic`, fake sockets expose `synthetic === true`, terminal
banners and log lines identify their synthetic origin, and installation prints
a synthetic-backend notice. Event envelopes keep the canonical wire shape;
their synthetic label is the transport, not an invented event field.

These fixtures exercise browser contracts without a Relay server. They cannot
prove real PTY, RFB/VNC, provider authentication, external notification delivery,
device behavior or deployed proxy behavior. `DeadSocket` always fails without
opening or producing desktop frames. A successful desktop-state HTTP fixture
is not a successful desktop connection. Existing simplified credential, service,
filesystem and provider fixtures remain synthetic approximations.

## Inventory and ownership

Before REL-034, `handlers.ts` contained the HTTP pattern table, first-match
dispatch and every feature handler; `index.ts` owned fetch/WebSocket patching;
`sockets.ts` contained event, terminal and log classes and the unavailable desktop
socket. Mutable data lived in exported fixture arrays/objects, the filesystem
map/trash, system RNG/processes, and private upload, file-job, TOTP and desktop
state. Timers drove latency, NDJSON chunks, jobs, open/close and socket intervals.
The existing tests checked documented route coverage and REL-031 contracts,
but did not define registration or comprehensive teardown isolation.

Now `features/*.mock.ts` contains eighteen independent owner modules. The
terminal and notification modules demonstrate explicitly typed request/response
registration and separate empty scenarios. Auth, files, uploads and apps keep
their private mutable state inside their registration closures. `owner.onReset`
restores it. Shared fixture utilities retain compatibility exports.

Feature owners edit their module and feature tests. A new default-exported
`*.mock.ts` module is discovered automatically by `modules.ts`; adding one
requires no edit to a common handler file. Modules are registered in ascending
owner ID order. The single integrator owns `index.ts`, `handlers.ts`, registry,
transport lifecycle and shared fixture reset. Shared fixture changes must be
coordinated with that integrator. No runtime dependencies are added.

## Registration contract

`defineMockModule(id, register)` stores a definition, not a shared scenario
instance. Each registry registration creates fresh owner controls, timer scope
and closure state. For example, the existing notification owner uses:

```ts
export default defineMockModule('notify', (owner) => {
  owner.http<unknown, Notification[]>('GET', '/notifications', () =>
    ok(owner.scenarios.state.empty ? [] : db.notifications),
  )
})
```

Use browser types mirroring `internal/api` for both generic arguments to
`owner.http<RequestBody, ResponseJSON>`. Handlers receive typed `body`, URL,
decoded path parameters, method and abort signal. They return `MockResponse`
or a promise, including the canonical `ErrorBody` alternative. Socket factories
receive a typed URL/parameter/protocol request and return a `FakeSocket` subclass:

```ts
owner.socket('/terminals/{id}/attach', ({ url }) => new FakeTerminal(url.href), {
  code: 1013,
  reason: 'terminal daemon disconnected',
})
```

Patterns accept relative API paths or full `/api/v1/` paths. Literal segments
win over parameters; equally specific patterns sort lexically, independently of
registration arrival. Literal punctuation is escaped and parameters are decoded.
Explicit HEAD registrations win; otherwise HEAD uses GET admission and headers
without a response body. HTTP and socket namespaces are separate.

Owner IDs must be unique. The same method and normalized parameter shape cannot
be registered twice: `/items/{id}` and `/items/{name}` conflict. Socket duplicates
use the same shape rule. Failed registration publishes no routes and cancels its
timers. Invalid patterns, unknown owners and missing HTTP/socket handlers throw
`MockRegistrationError` and log an error with the method/path/owner diagnostic.
Request query strings and bodies are not included in missing-handler diagnostics.
A registered handler may still return an ordinary contract 404 for a missing
entity; absence of the handler itself is an implementation failure.

Fetch interception covers only same-origin `/api/v1/` requests, including
`Request` objects and method/body overrides. External fetches are forwarded.
Socket interception additionally requires the origin's WS/WSS protocol and
host; other URLs/protocols are forwarded with their requested subprotocols.
`installMocks()` is idempotent and returns cleanup restoring exact globals and
any previous helper descriptor. Public health and auth setup/login/passkey-login
routes retain public admission; other HTTP routes and all sockets require the
synthetic authenticated state. Token-versus-cookie policy and CSRF verification
are not real-server proofs.

## Independent scenarios

Devtools expose `window.__relayMock.feature(owner)` and `.inventory()`.
`ScenarioControls.set` merges an isolated copy; explicitly set a field to
`undefined` to clear it, or call `.reset()` to clear all controls. Reading `.state`
also returns a copy. Controls affect only the selected owner:

```js
const terminal = window.__relayMock.feature('terminal')
terminal.set({ empty: true, latency: 250 })
window.__relayMock.feature('notify').set({ loading: true })
window.__relayMock.feature('notify').set({ loading: false })
terminal.set({ latency: [100, 10] }) // next two arrivals resolve out of order
terminal.set({ error: {
  status: 401,
  detail: { code: 'unauthorized', message: 'Synthetic expired session' },
} })
terminal.reset()
```

Loading holds requests until released, aborted or reset. Latencies must be
finite and nonnegative; queues are consumed in request-arrival order. Error
scenarios preserve `{error:{code,message,...}}`, optional `field`/`retryIn`, and
positive `Retry-After`. Use only statuses/codes documented for the endpoint being
exercised: e.g. terminal 401/unauthorized, auth login 429/rate_limited, or
500/internal. An error override applies to every HTTP route of its owner until
cleared; exercise the intended endpoint rather than claiming every route
supports that error. This is scenario injection, not a server error validator.

Owners explicitly implement empty response shapes: terminal/notification and
other list owners return arrays, agents preserve pagination, and files preserve
listing metadata with empty entries and zero counts. Empty controls do not erase
other owners' data. The events owner's empty control suppresses terminal
snapshots/activity on newly created sockets.

`feature('events').set({offline:true})` refuses new event sockets.
`window.__relayMock.disconnect('events')` closes existing ones; clear offline to
allow reconnect. The actual event-client abstraction retries, resends subscribed
topics and stops unsubscribed metrics. Terminal and log disconnects use their
documented 1013/1011 codes and reasons. `window.__relayMock.emit(type, data)`
injects typed synthetic public events; unknown event names remain compatible.
Backend-only audit, clip.capture and auth.session.revoked broadcasts are filtered.
Subscription/visibility, ping/pong and malformed-frame handling stay local to
each event socket. Global mock modes remain available for signed-out, setup,
TOTP, offline, empty, slow, live and network-down demonstrations.

## Reset and test isolation

Use `resetMocks()` before and after fixture tests. It aborts pending HTTP/body
reads and streams, clears/rebuilds the default registry, cancels owner jobs,
silently disposes fake sockets (including directly constructed ones), removes
property/native socket listeners, clears event-client handlers/status listeners,
subscriptions and reconnect timers, resets unauthorized listeners, IDs, event
sequence, fixture arrays/objects, filesystem/trash, system RNG/processes and
private owner maps/state. Exported root arrays/objects keep their identity.
Modes and session-storage mode state reset to defaults unless supplied explicitly.

`MockRegistry.reset()` retains definitions but resets its epoch, scenarios,
sockets, timers and owner reset callbacks. `.clear()` also removes registrations.
Independent registries do not share controls or owner closure state; default
feature modules still use the shared synthetic fixture database within a realm.
Parallel tests need isolated realms or their own data/registries, not concurrent
mutation of that singleton. Restore fake timers and globals after teardown.

Async extensions must use `request.signal`, abortable delays and `owner.later`
rather than unmanaged timers; check cancellation before mutating after awaited
I/O. A rejected response cannot stop arbitrary noncooperative extension code.
Accepted 202 jobs may outlive the request that admitted them, but never a reset.
NDJSON abort/cancel/reset cancels chunk timers and removes signal listeners.
Closing a connecting socket prevents its delayed open; Blob sends resolving
after disposal cannot reach feature callbacks.

Devtools `.reset(...modes)` resets then reloads the app to rebuild consumers whose
subscriptions were intentionally cleared. `.mode(...modes)` persists modes and
reloads. Programmatic reset during an app session requires consumers to remount.

`registry.test.ts` covers typed/duplicate/missing/deterministic registration,
independent registries, abort/loading/out-of-order responses and resets.
`runtime.test.ts` covers interceptor isolation, contract errors/auth, mutable
state, streams/listeners/timers and event-client disconnect/reconnect. Existing
route, event, timestamp and mock-contract suites retain REL-031 corrections and
REL-032 absent timestamps, including omitted running `FileJob.endedAt`.
All these checks are synthetic, including the event-client integration tests.
