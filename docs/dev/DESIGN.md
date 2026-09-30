# Relay design system — "Signal"

Relay is the relay station between you and your machine. The visual
language borrows from things people already read at a glance: the LED
dot‑matrix of a station departures board, the orange beacon on top of a
radio mast, the calm precision of an instrument panel. It must never look
like a generic AI dashboard: no purple gradients, no glassmorphism, no
glowing blobs, no emoji icons, no bento-for-the-sake-of-bento.

Principles, in priority order:

1. **Calm by default, loud only when it matters.** Neutral carbon and bone
   everywhere; the orange *signal* is reserved for "act now": the primary
   action on a screen and anything that needs the user.
2. **Every area is recognisable before it is read.** Each area has its own
   dot‑matrix glyph and hue (used sparingly: glyph, rail marker, header
   hairline, focus accents inside that area).
3. **Plain words.** "Needs you", "Running", "Previews", "Resume". Technical
   detail goes in a quieter mono "instrument" layer underneath.
4. **Touch first, keyboard fast.** 44 px targets on touch, full keyboard
   control on desktop, ⌘K everywhere.
5. **Fast is a feature.** Skeletons < 100 ms, no layout shift, lazy heavy
   modules (xterm, CodeMirror, noVNC), transitions ≤ 260 ms.

## Colour

Tokens live in `web/src/styles/tokens.css` as CSS custom properties on
`[data-theme="carbon"]` (dark, default) and `[data-theme="paper"]`
(light). The user's preference (`carbon | paper | auto`) is stored in
`data-theme-pref` and localStorage; `auto` resolves `data-theme` from the OS
and follows changes live. A boot script sets both attributes before first
paint, so there is never a flash of the wrong theme.

| Token | Carbon (dark) | Paper (light) | Use |
| --- | --- | --- | --- |
| `--bg` | `#0b0b0c` | `#f3f0e8` | app background |
| `--bg-sunken` | `#070708` | `#ebe7dd` | terminal, code, wells |
| `--surface-1` | `#111113` | `#faf8f3` | panels, cards |
| `--surface-2` | `#18181b` | `#ffffff` | raised controls, inputs |
| `--surface-3` | `#202024` | `#f0ede5` | hover |
| `--surface-4` | `#2a2a2f` | `#e6e2d8` | pressed, selected |
| `--line` | `rgb(237 233 224 / 0.08)` | `rgb(22 21 15 / 0.10)` | hairlines |
| `--line-strong` | `rgb(237 233 224 / 0.15)` | `rgb(22 21 15 / 0.18)` | borders, dividers |
| `--text` | `#ede9e0` (bone) | `#16150f` | primary text |
| `--text-2` | `#b3aea4` | `#4b4840` | secondary |
| `--text-3` | `#7c776f` | `#77736a` | meta, placeholders |
| `--text-4` | `#57534d` | `#a19d93` | disabled |
| `--signal` | `#ff5b1f` | `#e44a0f` | primary action, attention, brand |
| `--signal-ink` | `#ff7c4a` | `#c63d06` | signal-coloured text |
| `--signal-soft` | `rgb(255 91 31 / 0.14)` | `rgb(228 74 15 / 0.10)` | tinted backgrounds |
| `--on-signal` | `#0b0b0c` | `#ffffff` | text on signal fills |
| `--ok` | `#4fd18b` | `#1f9d5a` | success, working |
| `--warn` | `#f5b93e` | `#b77a06` | warnings |
| `--danger` | `#ff4d5e` | `#d42a3c` | destructive, errors |
| `--info` | `#6aa9ff` | `#2f6fd0` | links, info |

Area hues (`--area-<id>`), dark / light:

| Area | Hue | Light | Glyph idea (7×7 dots) |
| --- | --- | --- | --- |
| home | `#ede9e0` | `#16150f` | house / hearth |
| terminal | `#5fd89a` | `#1d8f55` | `>_` prompt |
| agents | `#ff7c4a` | `#d24a12` | four-point spark |
| files | `#f2c14e` | `#a8780a` | folder |
| code | `#6aa9ff` | `#2f6fd0` | `</>` brackets |
| desktop | `#57d4d1` | `#138a87` | window with title bar |
| previews | `#ff79b0` | `#c23a76` | eye / broadcast |
| system | `#b69cff` | `#6b4fd1` | pulse / meter bars |
| settings | `#9a958c` | `#6d6a62` | dial |

Status colours are semantic and identical in every area:

- **Working** — `--ok` dot with an expanding pulse ring (1.6 s).
- **Needs you** — `--signal` beacon, blinking (on 0.6 s / off 0.6 s), plus the
  words "Needs you". Never rely on colour alone.
- **Idle** — hollow `--text-3` ring.
- **Exited / done** — `--text-4` dot; failed exits use `--danger`.

Contrast: body text ≥ 7:1 on `--bg`, meta ≥ 4.5:1. Check both themes.

## Type

- **Mona Sans Variable** (`wdth` 75–125, `wght` 200–900) for UI and display.
- **JetBrains Mono Variable** for code, terminal, and the *instrument layer*
  (ids, paths, counts, timestamps, keyboard hints).
- Self-hosted via `@fontsource-variable/*`; subset to Latin; `font-display: swap`.

| Role | Size / line | Settings |
| --- | --- | --- |
| Display (empty states, big readouts) | 40/44 | wght 720, wdth 118, tracking -0.03em |
| Page title | 26/30 (22/26 mobile) | wght 650, wdth 110, tracking -0.02em |
| Section | 15/20 | wght 620 |
| Body | 14/20 (15/22 on touch) | wght 430 |
| Small | 13/18 | wght 450 |
| Instrument (mono) | 11.5/16 | wght 500, tracking 0.02em, `--text-3`, tabular nums |

Numbers are always `font-variant-numeric: tabular-nums`. Big metric
readouts use Mona Sans at `wdth 125` — wide, like a meter.

## Space, shape, depth

- 4 px grid. Spacing tokens `--s-1`…`--s-10` = 4, 8, 12, 16, 20, 24, 32, 40, 56, 72.
- Radii: `--r-xs 6px` (tags), `--r-sm 8px` (buttons, inputs), `--r-md 12px`
  (cards, panels), `--r-lg 18px` (sheets, dialogs), `--r-full`.
- Depth comes from surface steps and hairlines, not shadows. One shadow
  token for floating layers (menus, palette, sheets):
  `--shadow-float: 0 24px 64px -24px rgb(0 0 0 / 0.6), 0 0 0 1px var(--line-strong)`.
- Texture: an optional 3 % film-grain/dot texture on `--bg` (CSS radial
  gradient dots at 3 px pitch, 0.035 opacity) — the "dot field" identity.

## Layout

```
desktop ≥ 1024                                    mobile < 768
┌────┬──────────────────────────────────────┐     ┌──────────────────────┐
│ ●R │  Title             [⌘K Search…] 🔔  │     │ Title          ⌘K 🔔 │
│    ├──────────────────────────────────────┤     ├──────────────────────┤
│ ⌂  │                                      │     │                      │
│ >_ │  content (max 1180, or full-bleed    │     │ content              │
│ ✦  │  for immersive areas)                │     │                      │
│ ▢  │                                      │     │                      │
│ …  │                                      │     ├──────────────────────┤
│ ⚙  │                                      │     │ ⌂  >_  ✦  ▢  ···     │
└────┴──────────────────────────────────────┘     └──────────────────────┘
```

- **Rail** (desktop): 76 px, logo mark top, area items (glyph 20 px + mono
  label 10.5 px), notifications + settings + device bottom. Active item: glyph
  in area hue with dot-cascade animation, 2 px marker on the left edge.
- **Signal line**: a 2 px bar at the very top of the viewport in the current
  area hue. On navigation it sweeps left→right (the route progress
  indicator); while a terminal is working in view, a soft light travels
  along it. This is the "pulse" motif — use it nowhere else.
- **Header**: page title, contextual actions, search trigger (shows the
  shortcut), notifications bell with unread count.
- **Tab bar** (mobile): Home, Terminal, Agents, Files, More (sheet with the
  rest + settings). Safe-area aware. Hidden in immersive screens, where a
  compact top bar with a back affordance and session switcher appears.
- Breakpoints: `sm 480`, `md 768`, `lg 1024`, `xl 1440`.
- Immersive areas (terminal, code, desktop) are full-bleed; chrome
  collapses to the rail (desktop) or the compact bar (mobile).

## Components (`web/src/ui`)

Every component: keyboard accessible, visible focus ring (`2px` in
`--focus`, default `--text` at 60 %, area hue inside areas), works in both
themes, respects `prefers-reduced-motion`, has a story on `/dev/ui`.

Core: `Button` (primary · secondary · ghost · danger · icon; sm 28 / md 34 /
lg 44), `Input`, `TextArea`, `Select`, `Switch`, `Checkbox`, `Segmented`,
`Slider`, `Field` (label + hint + error), `Card`, `Panel`, `List` /
`ListRow` (leading, title, meta, trailing; 44 px min), `Table` (dense),
`Badge`, `Tag`, `StatusDot`, `AgentMark`, `Kbd`, `Tooltip`, `Popover`,
`Menu` (dropdown + context menu + long-press on touch), `Dialog`, `Sheet`
(bottom sheet on mobile with drag handle, side sheet on desktop), `Toast`
(stacked, swipe to dismiss), `Tabs`, `PathBar` (breadcrumb for files),
`EmptyState`, `Skeleton` (scan-line shimmer), `Progress` (bar + ring),
`Sparkline`, `DotMeter` (CPU/mem as a row of dots), `Glyph`, `DotText`
(5×7 dot font), `CodeBlock`, `Markdown`, `QR`, `Splitter`, `VirtualList`,
`Spinner` (three dots relaying a light), `ConfirmButton` (hold-to-confirm
for destructive actions on touch).

**AgentMark**: two-letter monogram (`CL` Claude, `CX` Codex, `GM` Gemini,
`OC` OpenCode, `KR` Kiro, `CU` Cursor, `GK` Grok, `PI` pi, `HM` Hermes,
`AM` Amp, `CP` Copilot, `AI` Aider, `QW` Qwen, `CR` Crush) in mono 600 on a
rounded square tinted with the agent colour (brand colours in
`web/src/lib/agents.ts`). No third-party logos.

**Glyph**: 7×7 bitmaps defined as strings (`"..#.#.."` rows). Renders SVG
circles (dot r = 0.9 of pitch/2); off-dots drawn at 8 % opacity so the grid
is always faintly visible. States: `idle`, `active` (dots light top→bottom,
40 ms per row), `pulse`.

## Motion

| Token | Value | Use |
| --- | --- | --- |
| `--d-1` | 120 ms | hover, press |
| `--d-2` | 180 ms | toggles, tabs |
| `--d-3` | 260 ms | enter/exit, sheets |
| `--d-4` | 420 ms | page-level, empty-state reveals |
| `--ease-out` | `cubic-bezier(0.16, 1, 0.3, 1)` | entering |
| `--ease-in` | `cubic-bezier(0.7, 0, 0.84, 0)` | leaving |
| `--ease-inout` | `cubic-bezier(0.65, 0, 0.35, 1)` | moving |

Patterns: route change = signal-line sweep + content 6 px rise/fade;
lists stagger 18 ms (first 10 items only); palette opens scale .985→1 +
fade 140 ms; sheets spring (stiffness 380, damping 36); beacon blink;
working pulse ring; number tickers roll digits (tabular). Use the View
Transitions API where available. Under `prefers-reduced-motion: reduce`
keep only opacity changes.

## Voice

- Sentence case everywhere. No exclamation marks. No "Oops".
- Say what happened and what to do: "Couldn't reach the machine. Retrying…"
- Names: Home, Terminal, Agents, Files, Code, Desktop, Previews, System,
  Settings. Statuses: Running, Needs you, Idle, Exited, Done, Failed.
- Empty states are invitations: one sentence + one primary action.

## Iconography

Area glyphs are custom (dot matrix). Utility icons use `lucide-preact` at
16/18 px, stroke 1.75, `currentColor`. Never mix icon sets.

## Refinements from building it (wave 1)

Decisions taken while implementing the kit, with the reason for each.
`UI_KIT.md` is the usage reference.

- **Carbon is the default, not Auto.** Relay is mostly opened on a phone
  next to a terminal, often at night; the dark instrument panel is the
  brand. Auto and Paper are one tap away (rail toggle on desktop, More sheet on phones, the
  palette: "Change theme").
- **Paper needs its own ink for brand colours.** Agent brand colours are
  tuned for carbon; on paper pale ones (Grok, Cursor) fell below 3:1. Agent
  marks on paper mix the brand colour 45 % into `--text` for the monogram,
  and skeletons use `--surface-4` so placeholders stay visible on white
  cards.
- **Meters and progress take fractions (0–1), always.** One convention
  across `DotMeter`, `Progress`, `ProgressRing` and `pct()` prevents the
  "every dot is red" bug where a percentage is passed.
- **The signal line is the only route progress indicator.** No spinners
  for navigation: the 2 px sweep in the destination area's hue plus a 6 px
  content rise, via View Transitions when supported. Within an area (e.g.
  `/terminal` → `/terminal/t1`) there is no transition — it would feel like
  leaving.
- **Immersive screens own the whole viewport.** On phones the header and
  tab bar are removed (not hidden) and the screen renders a `CompactBar`;
  on desktop the rail stays so the areas are one click away. Heights use
  `dvh` with `interactive-widget=resizes-content`, and `--vvh` tracks the
  visual viewport for keyboards on iOS.
- **Connection state is a pill in the header, never a modal.** "Reconnecting
  in 4s · Retry" on desktop; on phones only the countdown and Retry show
  (the full sentence stays for screen readers) so the title keeps its
  room. Retry has a 44 px hit area around a small visual button.
- **Login is top-anchored on phones** (`clamp(40px, 11dvh, 120px)` from the
  top) instead of centred, so the on-screen keyboard never shoves the form.
  The dot field is a canvas at ~24 fps (DPR ≤ 2) that pauses when hidden or
  off-screen and is static under reduced motion. The orange beacon on the
  field echoes the marketing site's "signal lands here" motif.
- **Commands close the palette themselves.** `ctx.close()` / `ctx.navigate()`
  is explicit so repeatable commands (copy, toggle) can keep it open —
  Raycast's behaviour for list items. Esc pops a view, then clears the
  query, then closes; ⌫ on an empty field pops.
- **Budget is enforced, not hoped for.** `pnpm build` prints the initial
  shell's JS (entry + static imports, gzip) and fails above 60 KB. Wave 1
  ships at ~51 KB; area screens, the palette, markdown, highlighting and QR
  are all separate chunks.
- **Mona Sans `wdth` is used for rhythm, not decoration:** 110 for titles,
  118 for display, 125 only for numeric readouts. Body stays at 100 for
  reading comfort on small screens.

