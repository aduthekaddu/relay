# Working agreement (humans and coding agents)

Read `docs/dev/ARCHITECTURE.md` first, then the spec for the area you are
changing (`docs/dev/API.md`, `FEATURES.md`, `DESIGN.md`, `TERMINAL_UX.md`,
`PTYD.md`, `SECURITY.md`, `SITE.md`). `internal/api/types.go` is the JSON
contract; `web/src/api/types.ts` mirrors it field for field.

## Safety on shared machines

- Develop against loopback ports `47700–47799` and an isolated
  `RELAY_HOME=~/.relay-<name>`. Never bind 80/443, never stop, restart or
  reconfigure services you did not start, never edit files outside this
  repository except inside your own `RELAY_HOME` or `/tmp`.
- Run heavy commands (`go test ./...`, builds, `pnpm install`) through
  `scripts/dev/safe`, which caps memory and CPU priority.
- Kill every process you started (dev servers, ptyd, browsers) before you
  finish. Do not use `pkill -f <pattern>` with patterns that could match
  your own shell; kill by PID.
- Screenshots: `node scripts/dev/shot.mjs <url> <out.png> [--mobile]
  [--full] [--frames 0,900,…] [--login user:pass]` uses its own browser
  profile (run `pnpm --dir scripts/dev install` once in a fresh checkout).
- Never commit secrets, real transcripts, real hostnames/IPs or personal
  paths. Test fixtures are synthetic.

## Code

- Go: standard library first; `gofmt`; `go vet`; errors wrapped with
  context; `context.Context` first parameter for anything that blocks;
  no global mutable state outside `main`/wiring; table-driven tests.
- New third-party Go modules: add a blank import to `internal/deps/deps.go`
  and run `go mod tidy` (keeps parallel branches from fighting over go.mod).
- New API types go in a new file `internal/api/<feature>.go` and
  `web/src/api/<feature>.ts` rather than editing shared files; if a shared
  struct needs a field, add it in one small, isolated hunk.
- Web: TypeScript strict, Preact function components + `@preact/signals`,
  plain CSS with the design tokens (no Tailwind, no CSS-in-JS runtime), no
  new runtime dependencies without a strong reason. Heavy modules (xterm,
  CodeMirror, noVNC, markdown, highlight.js) are lazy-loaded.
- Accessibility is not optional: labels, roles, focus order, contrast,
  reduced motion.

## Before you commit

`make check` (gofmt, vet, tsc, biome, tests) passes. Commits use
conventional prefixes (`feat(terminal): …`, `fix(auth): …`, `docs: …`),
small and focused. Describe user-visible changes in the body.

<!-- verify-relay:begin -->
## Proving changes

- Prove behaviour on the running app with the `verify-relay` skill (`.agents/skills/verify-relay/SKILL.md`; `/verify-relay` in Claude Code and Grok, `$verify-relay` in Codex).
- Read `.agents/skills/verify-relay/references/features/README.md` before driving. Update the feature file in the same PR when a user-visible path changes.
- `make check` is necessary, not sufficient: a change is done when its recipe closes VERIFIED.
- Evidence goes to `.proof/<UTC>-<slug>/` (gitignored) and must survive cleanup.
- Never drive the user's real instance. Unset `RELAY_SOCKET`, `RELAY_SESSION` and `RELAY_CONFIG` first.
- Stop only processes the control tool started (`node .agents/skills/verify-relay/control-relay.mjs down`), never by name.
<!-- verify-relay:end -->
