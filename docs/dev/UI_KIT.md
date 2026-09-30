# Relay UI kit — how to build a screen

The companion to `DESIGN.md` (the *why*). This is the *how*: every
component, hook and store you need to build an area screen, with props and
copy-paste examples. Every component has a live story on **`/dev/ui`**
(both themes side by side, and a phone frame) — open it next to this file.

```ts
import { Button, Card, EmptyState, ListRow, StatusDot, toast } from '../../ui'
import { terminalList, needsYou, subscribe, on } from '../../state'
import { usePageChrome, useImmersive } from '../../app/chrome'
import { api } from '../../api/client'
```

Ground rules (the kit enforces most of them for you):

- Plain CSS with the tokens in `web/src/styles/tokens.css`; put an area's
  styles in `routes/<area>/<area>.css` and import it from the route. No
  Tailwind, no inline colours, no new fonts, no second icon set.
- Colour only through tokens. Inside your area, `var(--hue)` is your area
  hue (set on `.shell`), so `color: var(--hue)` works anywhere below it.
  Signal orange (`--signal`) is reserved for the primary action on a screen
  and for things that need the user.
- Never rely on colour alone: `StatusDot label`, text + icon in toasts.
- Touch targets ≥ 44 px on touch (`--touch`); controls grow on coarse
  pointers automatically.
- Heavy modules (xterm, CodeMirror, noVNC, marked, highlight.js, qrcode)
  are imported lazily. The kit's `Markdown`, `CodeBlock` and `QR` already
  are — importing `../../ui` costs nothing extra.

## Tokens cheat sheet

| Group | Tokens |
| --- | --- |
| Surfaces | `--bg`, `--bg-sunken`, `--surface-1…4`, `--line`, `--line-strong`, `--scrim` |
| Text | `--text`, `--text-2`, `--text-3` (meta), `--text-4` (disabled) |
| Semantic | `--signal` `--signal-ink` `--signal-soft` `--on-signal`, `--ok(-soft)`, `--warn(-soft)`, `--danger(-soft)`, `--info(-soft)` |
| Areas | `--area-home` … `--area-settings`; `--hue` = current area |
| Type | `--font-sans`, `--font-mono`; `--fs-/--lh-` `display` `title` `section` `body` `small` `mono` |
| Space | `--s-1…--s-10` = 4 8 12 16 20 24 32 40 56 72 |
| Shape | `--r-xs 6` `--r-sm 8` `--r-md 12` `--r-lg 18` `--r-full`; `--shadow-float` (floating layers only) |
| Motion | `--d-1 120` `--d-2 180` `--d-3 260` `--d-4 420` ms; `--ease-out` `--ease-in` `--ease-inout` |
| Layout | `--rail-w`, `--header-h`, `--tabbar-h`, `--content-max`, `--safe-top/right/bottom/left`, `--vvh` (visual viewport height, tracks the on-screen keyboard) |
| Controls | `--ctl-sm 28`, `--ctl-md 34` (40 on touch), `--ctl-lg 44`, `--touch 44`, `--focus` |
| Layers | `--z-rail` < `--z-header` < `--z-popover` < `--z-sheet` < `--z-palette` < `--z-toast` < `--z-signal` |

Type classes: `.t-display`, `.t-title`, `.t-section`, `.t-body`, `.t-small`,
`.t-mono` (the instrument layer), `.t-readout` (wide Mona Sans for big
numbers). Utilities (kept deliberately small): `.row`, `.stack` (gap via
`--gap`), `.grow`, `.truncate`, `.tnum`, `.muted`, `.dim`, `.sr-only`,
`.grain`.

Themes: `<html data-theme="carbon|paper">`, preference in
`data-theme-pref` (`auto` follows the OS). Screenshot any page in a theme
with `?theme=paper`.

## Building an area screen

Your route folder (`web/src/routes/<area>/`) has `index.tsx` (default
export, lazy-loaded by `app/routes.ts`) and `commands.ts` (loaded eagerly:
keep it tiny, no heavy imports). Sub-paths are yours: read them with
`useRoute()` / `useLocation()` from `preact-iso`.

```tsx
import { useLocation } from 'preact-iso'
import { usePageChrome } from '../../app/chrome'
import { Button, EmptyState, List, ListRow, Panel, StatusDot, statusOf } from '../../ui'
import { terminalList } from '../../state'

export default function TerminalRoute() {
  const { path } = useLocation()
  usePageChrome({
    title: 'Terminal',
    subtitle: `${terminalList.value.length} sessions`,
    actions: <Button icon="plus" size="sm">New</Button>,
  }, [terminalList.value.length])

  if (!terminalList.value.length)
    return (
      <EmptyState glyph="terminal" hue="var(--hue)" title="No terminals yet"
        body="Start a shell or an agent; it keeps running when you close the tab."
        action={<Button variant="primary" icon="plus">New terminal</Button>} />
    )
  return (
    <Panel title="Running now" meta={`${terminalList.value.length}`} flush>
      <List label="Sessions">
        {terminalList.value.map((t) => (
          <ListRow key={t.id} href={`/terminal/${t.id}`} leading="glyph:terminal"
            title={t.title} subtitle={t.cwd} meta={t.pid}
            trailing={<StatusDot status={statusOf(t.activity, { attention: t.attention })} label />} />
        ))}
      </List>
    </Panel>
  )
}
```

### Page chrome — `app/chrome.ts`

| API | Notes |
| --- | --- |
| `usePageChrome({ title?, subtitle?, actions?, back? }, deps?)` | Sets the header while mounted. `title` defaults to the area label, `subtitle` is the mono meta line, `actions` render before search/bell, `back` (in-app path) shows ‹ on phones. Pass what changes as `deps` (default `[title, subtitle, back]`). |
| `useImmersive(on)` | Force immersive while mounted (e.g. a full-screen file preview). Routes declared `immersive: true` in `app/routes.ts` (terminal session, code, desktop) get it automatically. |
| `pageChrome`, `immersiveOverride` | The underlying signals (read-only for screens). |

Immersive mode: on phones the header and tab bar disappear and your screen
gets the whole viewport (`.content--bleed`, `100dvh`, no padding) — render a
`CompactBar` yourself. On desktop the header hides and the rail stays.
Size immersive content with `height: 100%`; use `var(--vvh)` when you must
follow the on-screen keyboard (terminal input). The viewport meta uses
`interactive-widget=resizes-content`, so `dvh` already shrinks with the
keyboard on Chrome/Android.

### Navigation — `app/nav.ts`

`navigate(url, { replace? })` for programmatic navigation (runs a View
Transition and the signal-line sweep when the area changes). Plain `<a
href="/files">` links are intercepted automatically — prefer them.

## Components (`web/src/ui`)

Props below list only the non-obvious ones; every component also takes
`class`. `Anchor` = an element or `{ x, y }` point. Icon strings everywhere
are `"glyph:<area>"`, `"agent:<id>"` or a registered lucide name.

### Signature pieces

**`Glyph`** — 7×7 dot bitmap, the identity of each area.
`name: GlyphName` (`home terminal agents files code desktop previews system
settings relay bell search plus check close alert spark empty`), `size=20`,
`state: 'idle' | 'active' | 'pulse'` (active = rows light top→bottom once;
pulse = lit dots breathe), `color` (default `currentColor`), `label` (omit
for decorative). `RelayMark` is the logo (`beacon` blinks it).

```tsx
<Glyph name="files" size={40} color="var(--hue)" state="active" />
```

**`DotText`** — 5×7 dot font: A–Z, 0–9 and punctuation (`. , : ; ! ? - / " ( ) + = * # % & @ < >`; the full set is `DOT_CHARS` in `ui/dotfont.ts`, unknown characters render blank).
`text`, `pitch=6` (px per dot), `color`, `grid=true` (faint unlit dots),
`reveal` (quick left→right scramble on mount). Use for hero words and
departures-board readouts, never for body text.

**`StatusDot`** — `status: 'working' | 'needs-you' | 'idle' | 'exited' |
'failed' | 'done' | 'offline'`, `label` (`true` = standard words, or a
string), `size: 'sm' | 'md'`. Working pulses, needs-you blinks (the
beacon), idle is a hollow ring. Helpers: `statusOf(activity, { attention,
exitCode })`, `statusLabel(status)`.

**`AgentMark`** — monogram tile. `agent` (id), `size: 'sm' 20 | 'md' 28 |
'lg' 40`, `color` (override with `AgentInfo.color`), `withName`. Data in
`web/src/lib/agents.ts`: `AGENTS`, `agentMeta(id)` (unknown ids get a
neutral mark derived from the id), `agentName(id)`.

**`DotMeter`** — CPU/mem as a row of dots. `value` **fraction 0–1**,
`dots=10`, `warn=0.75`, `danger=0.9`, `color`, `label` (required),
`valueText`, `size`. Role `meter`.

**`Sparkline`** — `values`, `width=120`, `height=32`, `min`/`max` (fixed
range), `color`, `fill`, `dot` (mark latest), `label` (a sentence with the
current value). `sparkPath()` is exported for custom charts.

**`Spinner`** — three dots relaying a light. `size: 'sm' | 'md' | 'lg'`,
`label`.

### Controls

**`Button`** — `variant: 'primary' | 'secondary' | 'ghost' | 'danger' |
'icon'` (default secondary), `size: 'sm' | 'md' | 'lg'`, `icon`, `iconEnd`,
`loading` (keeps width), `label` (required for `icon`: aria-label +
tooltip), `href` (renders `<a>`), `type`. One `primary` per screen.

```tsx
<Button variant="primary" icon="plus" onClick={create}>New terminal</Button>
<Button variant="icon" icon="ellipsis" label="More actions" />
```

**`ConfirmButton`** — destructive actions. Desktop: click, then "Click
again to confirm" for 3 s. Touch: hold for `holdMs=900` with a filling bar
and a haptic tick. `onConfirm`, `confirmLabel`, `holdLabel`, `icon`,
`size`, `disabled`. Use `Dialog role="alertdialog"` instead when the action
needs an explanation.

**Form** — wrap controls in `Field` for label/hint/error wiring
(`aria-describedby`, `aria-invalid` are automatic):

```tsx
<Field label="Branch" hint="Created from main." error={err}>
  <Input mono value={branch} onInput={(e) => setBranch(e.currentTarget.value)} />
</Field>
```

| Component | Props |
| --- | --- |
| `Field` | `label`, `hint`, `error`, `hideLabel`, `aside` (right of the label, e.g. "Forgot?"), `id` |
| `Input` | all `<input>` props + `size`, `icon` (leading), `trailing` (node inside the field), `invalid`, `mono`; forwards `ref` |
| `TextArea` | `<textarea>` props + `autoGrow`, `maxRows=8`, `invalid`, `mono` |
| `Select` | `options: {value,label,disabled?}[]`, `value`, `onChange(value)`, `size`, `invalid` (native select: best on phones) |
| `Switch` | `checked`, `onChange(bool)`, `label`, `disabled` (role switch) |
| `Checkbox` | `checked`, `onChange(bool)`, `label`, `indeterminate`, `disabled` |
| `Segmented` | `options: {value,label,icon?,aria?}[]`, `value`, `onChange`, `label` (group name), `size` — radio group with roving focus |
| `Slider` | `value`, `onChange`, `min=0`, `max=100`, `step=1`, `label`, `format(v)` (shows a readout) |

### Layout and content

| Component | Props / notes |
| --- | --- |
| `Card` | `pad: 'none' 'sm' 'md' 'lg'`, `tone: 'default' 'signal' 'danger' 'sunken'`, `href` (whole card is a link). Signal tone = "needs you". |
| `Panel` | Section with header: `title`, `meta` (mono), `actions`, `level: 2 | 3`, `flush` (no padding, for lists/tables). |
| `List` / `ListRow` | `List label dividers`. `ListRow`: `leading` (icon string or node), `title`, `subtitle`, `meta` (mono, right), `trailing`, `href` or `onClick`, `onContextMenu`, `selected`, `disabled`. 44 px min height. |
| `Table<T>` | `columns: {key, header, render(row), numeric?, width?, hideBelow?: 'sm' | 'md'}[]`, `rows`, `rowKey`, `onRowClick`, `caption` (required, visually hidden), `empty`. Dense, tabular nums. |
| `Tabs<T>` | `items: {id,label,count?,href?}[]`, `value`, `onChange`, `label`. With `href` the tabs are links (state in the URL). |
| `PathBar` | `value` (path), `home` (shows `~`), `hrefFor(prefix)` → link for each segment. Collapses the middle on long paths. |
| `Badge` | `tone: 'neutral' 'signal' 'ok' 'warn' 'danger' 'info'`, `solid` |
| `Tag` | `color` (dot), `onRemove`, `removeLabel` |
| `Kbd` | `keys: string[]`, e.g. `['mod','K']` → ⌘ K / Ctrl K |
| `EmptyState` | `glyph`, `hue`, `title`, `body` (one sentence), `action` (one Button), `size` |
| `Skeleton` | `width`, `height=14`, `lines`, `radius`. Show within 100 ms; match the final layout so nothing shifts. |
| `Progress` | `value` **fraction** (omit = indeterminate), `label`, `tone` |
| `ProgressRing` | `value` **fraction**, `size=44`, `stroke=4`, `label`, `tone`, children in the middle |
| `Splitter` | `direction: 'row' | 'column'`, `first`, `second`, `initial=0.5`, `min=160`, `storageKey` (persists), `label`. Keyboard: arrows on the divider. |
| `VirtualList<T>` | `items`, `itemHeight` (fixed), `render(item, i)`, `itemKey`, `overscan=6`, `onEndReached`, `endThreshold`, `label`. `ref` → `{ scrollToIndex(i, align), element }`. Give the parent a height. |
| `CompactBar` | Immersive top bar: `title`, `subtitle`, `status`, `actions`, `back`, `onTitleClick` (session switcher). |
| `CodeBlock` | `code`, `lang` (id, alias or extension), `filename`, `lineNumbers`, `wrap`, `maxHeight`, `copy=true`. highlight.js core + only the needed language, lazily. |
| `Markdown` | `source` (untrusted — sanitised with DOMPurify; links get `rel=noopener`), `compact`. Lazy (marked + DOMPurify load on first use). For highlighted code use `CodeBlock`. |
| `QR` | `value`, `size=176`, `label`. Lazy qrcode-generator, dot modules. |
| `Icon` | `name`, `size=16`, `label`. Register extra lucide icons where you use them: `registerIcons({ 'git-branch': GitBranch })`. |

### Overlays

All overlays render into `#layers` via `Portal`, trap focus, restore focus
on close, close on Esc (topmost only), and respect reduced motion.

**`Dialog`** — `open`, `onClose`, `title`, `description`, `footer`
(buttons, primary last), `role: 'dialog' | 'alertdialog'`, `size`,
`dismissable=true` (scrim click; set false for forms).

**`Sheet`** — `open`, `onClose`, `title`, `label`, `side: 'auto' |
'bottom' | 'right'` (auto = bottom on phones, right on desktop), `width=420`,
`footer`, `bare`. Bottom sheets have a drag handle, spring physics and
flick-to-dismiss.

**`Menu`** / **`MenuButton`** — `items: MenuItem[]` where `MenuItem = { id,
label, icon?, shortcut?, hint?, danger?, disabled?, checked?, onSelect() }`
and `SEPARATOR` divides groups. `Menu` takes `open`, `onClose`, `anchor`,
`label`, `placement`, `sheetOnTouch=true` (becomes a bottom sheet on touch).
`MenuButton` is a `Button` that owns its menu (`menuLabel`). For rows:

```tsx
const menu = useContextMenu(() => [
  { id: 'rename', label: 'Rename', icon: 'pencil', onSelect: rename },
  SEPARATOR,
  { id: 'kill', label: 'Kill session', danger: true, onSelect: kill },
], 'Session actions')
<div {...menu.handlers}>                     {/* right-click + long-press (450 ms) */}
  <ListRow title={t.title} />
</div>
{menu.menu}
```

`useLongPress(fn, ms)` is available on its own.

**`Popover`** — `open`, `onClose`, `anchor`, `placement='bottom-end'`,
`label`, `trapFocus=true`. **`Tooltip`** — `content`, `placement='top'`,
`delay=450`, one element child (gets `aria-describedby`). Not shown on touch.

**Toasts** — `toast(message, { kind?: 'info' | 'success' | 'warning' |
'danger' | 'attention', body?, action?: { label, onClick }, duration?, id? })`
returns the id; `dismissToast(id)`. Pass the same `id` to update in place
("Uploading… 40%" → "Uploaded"). `<Toaster />` is already in the shell.
Swipe to dismiss; danger/attention toasts are `role=alert`.

Hooks from `ui/overlay`: `useMedia(query)`, `Portal`, and for custom
floating UI `useFocusTrap`, `useDismiss`, `useAnchoredPosition`, `place`.

## Live data — `web/src/api/events.ts` + `web/src/state`

One WebSocket to `/api/v1/events` for the whole app, opened after sign-in.
It reconnects with jittered exponential backoff (0.5 s → 10 s, 30 s when
the browser is offline), pings every 25 s, treats 65 s of silence as dead,
re-sends subscriptions after every reconnect and reports the visible path +
document visibility (so the server can suppress push notifications for
what you are looking at).

```ts
import { on, subscribe } from '../../api/events'

useEffect(() => subscribe('metrics'), [])                 // ref-counted; returns unsubscribe
useEffect(() => on('metrics', (m) => { cpu.value = m.cpu }), [])  // typed by event name
```

| API | Notes |
| --- | --- |
| `on(type, fn)` | `fn(data, event)`; `type` is any `EventType` or `'*'`. Returns an unsubscribe. Handler errors are caught and logged. |
| `subscribe(topic)` | Topics the server only streams on request (`metrics`, `agents.usage`, …). Ref-counted across callers; idempotent unsubscribe. |
| `events.dispatch(ev)` | Emit locally (optimistic UI, mocks). |
| `events.onStatus(fn)` / `getStatus()` | Raw socket state; screens should use `connection` instead. |

Stores (`@preact/signals`; read `.value` in render and components update):

| Store | Type | Notes |
| --- | --- | --- |
| `auth` | `Signal<AuthState \| null>` | `loadAuth()`, `signOut()` |
| `info` | `Signal<Info \| null>` | host, version, features; refreshed on `info` events |
| `connection` | `Signal<{ state: 'connecting' \| 'online' \| 'reconnecting' \| 'offline', failures, since, retryAt? }>` | `isOnline` (computed), `retryConnection()` |
| `terminals` | `Signal<ReadonlyMap<string, TerminalSession>>` | kept current from `terminal.*` events; `terminalsLoaded` |
| `terminalList` | computed | sorted: needs you → working → pinned → most recent output |
| `needsYou`, `working` | computed | drive badges, the tab bar dot and the signal-line shimmer |
| `notifications` | `Signal<Notification[]>` | newest first, deduped; `unreadCount`, `notificationsLoaded`, `markRead(ids)`, `markAllRead()`, `removeNotification(id)` |
| `theme`, `themePref` | `'carbon' \| 'paper'`, `+ 'auto'` | `setTheme(pref)`, `toggleTheme()` |

Stores are never written by screens directly; call the API and let the
event update the store (or `upsertTerminal()` for optimistic changes).

HTTP: `api.get<T>(path, init?)`, `api.post/put/patch<T>(path, body?, init?)`,
`api.del<T>(path, init?)` from `api/client.ts` — paths are relative to
`/api/v1/` (`init.signal` for aborts); `qs({...})` builds query strings,
`seg(s)` encodes a path segment, `wsUrl(path)` makes a socket URL. Errors
throw `ApiError` with `status`, `code`, `message`, `field`, `retryIn`; a 401
sends the user to `/login?next=…`.

## Command center — `web/src/command`

Every area contributes commands from `routes/<area>/commands.ts`; they are
registered at startup by `command/all.ts`. The palette ranks them with
fuzzysort (title > keywords > subtitle, bonuses for priority, prefix and
recent use), merges server results from `/api/v1/search` (debounced 80 ms,
stale requests aborted) and any registered providers.

```ts
// routes/terminal/commands.ts — keep imports light: this file is eager.
import type { Command } from '../../command/registry'
import { api } from '../../api/client'

export const commands: Command[] = [
  {
    id: 'terminal.new',                 // "<area>.<verb>", also the recents key
    title: 'New terminal',
    section: 'Terminal',
    area: 'terminal',
    icon: 'glyph:terminal',
    keywords: ['shell', 'tty'],
    shortcut: ['mod', 'shift', 'T'],    // display only; bind it yourself
    args: [{ name: 'cwd', placeholder: 'Directory', optional: true }],  // inline chips
    suggested: true,                    // shown on the empty palette
    run: async (ctx) => {
      const t = await api.post<{ id: string }>('terminals', { cwd: ctx.args.cwd })
      ctx.navigate(`/terminal/${t.id}`)  // navigate() also closes the palette
    },
    actions: [                          // ⌘K / → opens the action panel; first = ⌘Enter
      { id: 'agent', title: 'New agent session…', icon: 'glyph:agents', run: (ctx) => ctx.navigate('/agents/new') },
    ],
  },
]
```

`CommandContext` (`ctx`): `navigate(path)`, `close()`, `push(view)`,
`pop()`, `toast(message, kind?)`, `copy(text, what?)`, `args`, `query`.
**Commands close the palette themselves** — call `ctx.close()` (or
`ctx.navigate`) when done; staying open is the right default for things
like "Copy" that the user may repeat.

`when: () => boolean` hides a command contextually (e.g. only in an area).
`priority` breaks ties. `view` makes Enter push a sub-view instead of
running.

### Sub-views (Raycast "push")

```ts
const branches: PaletteView = {
  id: 'git.branches',
  title: 'Branches',
  placeholder: 'Filter branches…',
  // Items can be async; the signal aborts when the query changes.
  items: async (q, signal) => (await api.get<Branch[]>(`workspaces/git/branches${qs({ q })}`, { signal }))
    .map((b) => ({ id: `branch:${b.name}`, title: b.name, icon: 'git-branch',
                   run: (ctx) => { checkout(b); ctx.close() } })),
  filter: true,    // fuzzy-filter items by the query (default)
}
// or a fully custom view: component: () => import('./QuickAI')  (default export gets { ctx, query, pop })
```

Esc / ⌫ on an empty field pops; Esc at the root clears the query, then
closes.

### Providers (search sources, extensions)

```ts
import { registerProvider } from '../../command/registry'

registerProvider({
  id: 'calculator',
  section: 'Calculator',
  debounce: 0,        // synchronous providers answer instantly
  minQuery: 1,        // 0 = also on the empty query
  top: true,          // above commands
  fuzzy: false,       // keep provider order (true = ranked with everything else)
  search: (q) => { const v = evaluate(q); return v === null ? [] :
    [{ id: 'calc', title: String(v), subtitle: q, icon: 'equal', remember: false,
       actions: [{ id: 'copy', title: 'Copy answer', run: (ctx) => ctx.copy(String(v), 'Answer') }] }] },
})   // returns an unregister function
```

Extensions (clipboard history, snippets, notes, Quick AI, calculator,
scripts) are commands + providers + views registered from their own module;
add the module's registration call to `command/all.ts`. `PaletteItem`
fields available to providers: `title`, `subtitle`, `icon`, `status`
(StatusDot), `section`, `accessory` (mono, right), `shortcut`, `keywords`,
`args`, `run` / `link` / `view`, `runLabel`, `actions`, `remember=true`,
`priority`, `score`.

Open programmatically: `openPalette({ query?, view? })`, `closePalette()`,
`togglePalette()`. Recents: `recents`, `rememberItem`, `forgetRecent`,
`clearRecents` (localStorage, 8 entries).

Global shortcuts (in `app/shortcuts.ts`): ⌘K / Ctrl K palette, `G` then
the area's letter (`goKey` in `app/areas.ts`: `G T` terminal, `G A` agents,
`G F` files, …; shown next to each area in the palette), `?` shortcut
sheet, Esc closes the top layer. Letter shortcuts pause while typing in a
field or while a modal is open.

## Mock backend — `pnpm dev:mock`

`VITE_MOCK=1` (the `dev:mock` script) swaps `fetch` and `WebSocket` for an
in-browser backend typed against `web/src/api/types.ts`: every endpoint in
`docs/dev/API.md` (a test fails if one is missing), a fake events socket
(metrics that move, terminal activity changes, notifications), fake
terminal and log sockets, a writable in-memory file tree, git status,
previews, agents with transcripts, usage and quotas, and search.

Scenarios via `?mock=` (comma-separated, sticky per tab, `?mock=` resets):
`signed-out`, `setup` (first run), `totp`, `empty`, `offline`, `slow`,
`live` (a notification every 20 s), `down` (API unreachable).
`window.__relayMock` in devtools: `mode('setup', 'slow')` (reloads),
`modes()`, `notify()` (a live notification), `emit(event)`.

Add data for a new endpoint in `mocks/handlers.ts` (route table at the
top, synthetic data in `mocks/data.ts`, file tree in `mocks/fs.ts`). All
data is synthetic — no real hostnames, paths or transcripts.

## Checks

```sh
cd web
pnpm typecheck && pnpm lint && pnpm test   # tsc strict, biome, vitest
pnpm build                                 # prints the initial-shell JS size; fails above 60 KB gzip
```

Visual QA: `VITE_MOCK=1 pnpm dev`, then
`node scripts/dev/shot.mjs http://127.0.0.1:47780/<path> /tmp/x.jpg [--mobile] [--full]`
with `?theme=paper` for the light theme.
