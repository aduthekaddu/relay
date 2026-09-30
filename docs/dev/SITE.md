# relay site — storyboard & build spec

The site sells Relay in ten seconds and documents it for years. It lives in
`/site` (Astro, static output, deployed to GitHub Pages). Docs are
Starlight under `/docs`, sourced from the repo's `/docs` folder so GitHub
and the site show the same text.

## What great looks like (research notes)

From recent.design (Astro Dither, Interfere, Displace, Nothing to Watch,
Orior AI), landing.love, 60fps.design, saaspo, minimal.gallery, and the
dev-tool sites known for craft (ghostty, linear, raycast, warp, zed,
cursor, vercel, teenage.engineering):

- **One idea, visible in the first second.** Ghostty's animated ASCII ghost
  says "terminal" before a word is read. The hero is the product's
  *metaphor*, not a screenshot. For us: the live dot field.
- **Restraint is the luxury signal.** The featured sites use one or two
  colours and a lot of black. The accent appears only where the eye must
  go (Astro Dither: dithered monochrome + one hue).
- **Texture beats gradients.** Dither, halftone and dot grids (Interfere,
  Nothing) feel physical and "made"; soft blurred blobs read as template.
- **Type is the layout.** Linear and Vercel set huge, tightly-tracked
  headlines with generous negative space and let the copy do the selling;
  body copy is short, grey, and never competes.
- **Scroll is a timeline, not a list.** The memorable sites pin a scene and
  scrub it (Warp's terminal, Raycast's palette): each "shot" teaches one
  thing, then gets out of the way.
- **Show the product doing the thing.** Raycast types real queries into a
  real-looking palette; Zed shows real code moving. Scripted, deterministic
  demos beat videos (crisp, tiny, accessible, scrubbable).
- **An instrument layer.** Linear's "FIG 0.1" captions and TE's spec labels:
  small mono metadata next to big type gives precision and rhythm.
- **Industrial precision (TE).** Physical-object metaphors (keys, knobs,
  displays) rendered with care become the brand. Ours: the split-flap
  board and the beacon.
- **Motion with weight.** Expo-out reveals, line masks, 0.02–0.04 s
  staggers; nothing bounces except things that are physical (flaps).
- **Performance is part of the design.** The best of 60fps.design are
  canvas/WebGL but ship text first; a janky hero kills the effect.
- **Mobile is its own composition,** not a squashed desktop: fewer dots,
  stacked type, the demo at full width (Cursor, Linear on phones).
- **Consistent system across pages.** Product pages reuse the same shots
  with a different accent hue; coherence reads as quality.
- **A finale that converts.** Vercel/Warp end on one enormous command or
  button; the install line is the last big type on the page.

Direction chosen: carbon night, a live LED dot field that forms type,
devices and dithered photographs; one orange beacon; Mona Sans at display
sizes with width-axis motion; departures-board flaps as the "every agent"
object. No gradients, no glass, no 3D.

## The idea in one line

**Your machine, relayed to every screen.** The site is a short film about a
signal leaving a machine at night and arriving in your pocket: an LED dot
field (the "signal") that forms type, devices, boards and images, a single
orange beacon that means "your agent needs you", and huge, confident type
set in the calm spaces the field leaves open.

References to anchor the look (study, don't copy): recent.design picks
"Astro Dither" (dithered imagery, restraint) and "Interfere" (halftone +
live waveform); ghostty.org (the animated ASCII hero sells a terminal in
one glance); Nothing's dot-matrix typography; railway/airport split-flap
departure boards; Teenage Engineering product pages (industrial
precision); Linear (type discipline). Our twist: the dot field is *live*
(WebGL), reacts to the pointer, and morphs between shots.

## Visual system

- Palette: carbon `#0b0b0c`, bone `#ede9e0`, signal `#ff5b1f`, plus the app
  area hues for product pages (each product page takes its area hue as a
  secondary accent — terminal green, agents orange, files amber, desktop
  cyan, previews pink, system lilac).
- Type: Mona Sans Variable for display (animate `wdth` 75↔125 and `wght`
  on scroll/hover for kinetic headlines), JetBrains Mono for labels,
  commands and readouts. Headlines are big: `clamp(44px, 9vw, 168px)`,
  tracking −0.04em, line-height 0.92.
- Imagery: generated photographs (see Assets) *always* shown through the
  dot/dither treatment in carbon + bone (+ orange highlight), never raw.
  This keeps every image on-brand no matter which model produced it.
- Grain: fine dot texture across the page (3 % opacity).
- Composition rule: where the headline sits, the field goes quiet (dims to
  15 % and slows). Loud field = small text; big text = quiet field.

## Motion system

- Lenis smooth scroll (respect reduced motion: native scroll).
- GSAP + ScrollTrigger for pinned, scrubbed "shots". SplitText for line /
  character reveals (mask-up, 0.9 s, expo-out, 0.02 s stagger).
- The LED field (`LedField`, WebGL2, instanced points): a grid of dots
  whose brightness/colour come from a target texture (canvas-rendered text,
  shapes, images). Morph between targets with a noise-driven dissolve;
  pointer = ripple lens (dots brighten + grow within radius). Dot pitch
  6–10 px desktop, larger on mobile; capped at ~20k dots; pauses when
  off-screen or tab hidden; DPR ≤ 2.
- Split-flap (`FlapBoard`): DOM characters with 3D flip halves, 60 ms per
  flip, cascading left→right; statuses change on a timer.
- Beacon: the orange dot — blinks 1.2 s; on "needs you" beats it emits a
  ring through the LED field.
- Page transitions: Astro view transitions; the field persists across
  pages (transition:persist) and morphs to the next page's opening target.

## Pages

### `/` — Home (the film)

1. **Signal acquired (hero, 100vh).** Preloader: dots scramble into
   `RELAY` (dot font) in < 1.2 s, then spread into the field showing a slow
   interference wave (two radio sources). Left third, huge:
   **Leave the desk.** / **Keep the machine.** Sub: "Relay turns your
   computer or server into a private workspace you can open anywhere —
   terminals that work on a phone, every coding agent in one place, files,
   previews and a full desktop. One binary. One command to install."
   CTAs: `Get Relay` (copies the install one-liner; shows "Copied") and
   `See how it works` (scrolls). Bottom-right: a live mini departures
   ticker (mono): `CLAUDE  refactor-auth   RUNNING`, `CODEX  flaky-test
   NEEDS YOU ●`. The pointer ripples the field.
2. **Every screen (pinned).** The field forms phone → tablet → laptop
   outlines as you scroll; headline "One machine. Every screen." with three
   short facts (installable app, works on 3G, no client to install).
3. **The terminal (pinned, scrubbed).** An HTML phone mockup with a
   scripted terminal session (an agent working, then asking a question).
   The mobile key bar slides up; callouts appear in sequence with leader
   lines: "Keys you actually need", "Swipe to move the cursor", "⌘⌫ deletes
   the line — like your Mac", "Paste a screenshot, get a path", "Close the
   tab — it keeps running". Headline: "A real terminal. Even on a phone."
4. **The board.** Full-width split-flap departures board, 6 rows of agents
   and tasks, statuses flipping. Headline: "Every agent. Every session.
   One board." Below: three columns (Live and past sessions, Resume in one
   tap, Search every conversation) with tiny dot glyphs.
5. **The tap on the shoulder.** The field goes dark; a single beacon pulses
   at centre; enormous type: "It taps you on the shoulder." A lock-screen
   notification drops in: "codex needs you — approve `pnpm test`?" Sub:
   "Relay notices when an agent is waiting — through hooks or the terminal
   itself — and pings your phone. One tap and you're back."
6. **⌘K for everything.** A Raycast-style command center mock types
   queries (`resume auth`, `kill :3000`, `open preview 5173`,
   `ask why is CI red`) with results animating. Headline: "One shortcut for
   your whole machine."
7. **See it running.** Split: Previews (port list + QR drawn in dots,
   "Your dev server, on your phone, over HTTPS") and Desktop (dithered
   Chrome + Blender screenshot, "A real desktop for when agents need eyes").
8. **Night shift.** A horizontal 22:00 → 07:00 timeline with scheduled
   runs; results pop up at dawn. "Give your agents a night shift."
9. **Locked down.** Spec-sheet list in big type with mono footnotes: one
   port · passkeys · argon2id · sessions you can revoke · zero telemetry ·
   everything stays on your machine.
10. **Light.** Instrument readouts (real numbers from the release build):
    binary size, idle memory, cold start, app shell JS.
11. **Install.** The one-liner, enormous, with a copy button; an animated
    install transcript; three steps (Get a machine · Run one line · Open it
    on your phone). Links to guides for VPS, home server, Tailscale, Mac.
12. **Footer.** LED `RELAY`, nav, GitHub, "MIT licensed. Made for people
    who ship from anywhere."

### Product pages (same system, own accent)

- `/terminal` — mobile terminal deep dive: key bar, gestures, compose bar
  with dictation, shortcuts table (Mac/Windows/Linux), image paste,
  persistence, recordings & replay, OSC 52 clipboard, `relay` CLI.
- `/agents` — board, attention (hooks per agent), history + full-text
  search, transcript reader, resume/fork, worktrees for parallel agents,
  diff review, usage & quotas, supported agents grid.
- `/command` — the command center: search everything, actions, arguments,
  script commands (Raycast-compatible metadata), clipboard history,
  snippets, notes, Quick AI with your own CLI subscriptions, calculator.
- `/desktop` — desktop + Chrome + Blender + agent computer use; previews
  (subdomain/path, QR, auto-detect, localhost link rewriting); Code (VS Code).
- `/security` — threat model in plain words, architecture diagram (dot
  style), defaults, how to expose safely (Tailscale, Cloudflare Tunnel,
  public HTTPS), responsible disclosure.
- `/install` — the one-liner, then tabs: "I have a VPS", "My home
  computer", "Only on my tailnet", "macOS"; FAQ.
- `/docs/*` — Starlight, themed to match (carbon/paper, Mona Sans, dot
  details), search via Pagefind.
- `404` — the field shows "NO SIGNAL" in dots; link home.

## Assets

- `scripts/gen-images.sh` documents the prompts used with `codex exec`
  (image generation) so images can be regenerated. Brief for every image:
  night, long-exposure, cinematic, very dark, minimal, one warm light
  source, lots of negative space, no text, no logos.
  Subjects: lattice relay tower on a hill (hero texture), hands holding a
  phone on a night train, a desk with a sleeping laptop and one lit
  monitor, server rack detail, a road through dark hills with a single car
  light, city window grid at 3 a.m.
- `site/scripts/dither.mjs` converts sources to the house treatment
  (Atkinson dither, duotone carbon/bone, optional orange highlight mask)
  and writes AVIF/WebP at 1×/2×. Originals are kept out of the repo (large);
  processed outputs are committed.
- OG images (1200×630) rendered from a dedicated page with Playwright.

## Budgets

- LCP < 1.8 s on 4G mid-range phone; CLS < 0.02; TBT < 150 ms.
- Home JS ≤ 160 KB gzip (GSAP + Lenis + field + page code); product pages
  ≤ 100 KB. WebGL initialised after first paint, never blocking text.
- Every animation has a reduced-motion variant; the page is fully
  readable with JS disabled (static first frame + text).
- Accessible: semantic headings, focus styles, alt text, sufficient
  contrast (bone on carbon), `lang`, skip link.
