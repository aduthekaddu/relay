# Timestamp contract and migration (REL-032)

The JSON contract lives in the Go types and their browser mirrors. Optional
value timestamps use `json:"…,omitempty,omitzero"`: Go's `time.Time.IsZero()`
omits unavailable values; known values still use `time.Time`'s RFC 3339 JSON
encoding. The repository requires Go 1.27, which supports `omitzero`.
No producer, database schema, timestamp conversion or generic event decoder
changes in this migration.

## Inventory

Before this change all fourteen fields below used `time.Time` with only
`omitempty`, which actually emitted `"0001-01-01T00:00:00Z"` for zero values.
After this change zero values are absent. Each browser twin already uses
`?: string`; defined values retain their offset and up to nine fractional
digits. These fields alone change their server representation.

| Go field / JSON field | Canonical source of a value or absence |
| --- | --- |
| Passkey.LastUsedAt / lastUsedAt | `internal/auth/passkeys.go`, `accounts.go`: unused credential/storage zero maps to zero time; use records a value |
| APIToken.LastUsedAt / lastUsedAt | `internal/auth/accounts.go`: new/never-used token; storage `fromMS(0)` returns zero |
| TerminalSession.LastOutputAt / lastOutputAt | `internal/ptyd/session.go`: initial/imported metadata may be partial; output records UTC |
| TerminalSession.LastInputAt / lastInputAt | `internal/ptyd/session.go`: input records UTC; initial metadata has no input time |
| TerminalSession.ExitedAt / exitedAt | `internal/ptyd/session.go`, `persist.go`: running has zero; exit or lost-session transition records a time |
| AgentMessage.At / at | `internal/agents/history.go` and adapters: `parseTime` returns zero for unavailable/invalid native inputs; known inputs retain the existing conversion to UTC |
| QuotaWindow.ResetsAt / resetsAt | `internal/agents/quotas.go`: optional native epoch/relative reset or parsed Claude reset; unavailable reset remains zero |
| Workspace.LastUsedAt / lastUsedAt | `internal/workspaces/workspaces.go`: aggregate known terminal, agent and history activity; unused workspace remains zero |
| Service.Since / since | `internal/system/units.go`: boot/monotonic activation data; unavailable data remains zero |
| App.Since / since | `internal/apps/apps.go`, `capabilities.go`: lifecycle owner's known start time; stopped/unavailable snapshot has zero |
| Schedule.NextRun / nextRun | `internal/schedule/schedule.go`: enabled valid schedule's calculated next activation; disabled/unavailable next run is zero |
| ScheduleRun.FinishedAt / finishedAt | `internal/schedule/runner.go`, `schedule.go`: running/storage zero; finished and skipped transitions record a time |
| SearchResult.At / at | feature search producers (`terminal`, `agents`, `workspaces`, `snippets`, `notes`): known source activity/update; untimed results leave zero |
| FileJob.EndedAt / endedAt | `internal/files/jobs.go`: running is zero; done/failed/canceled completion records UTC |

Two optional representations already work and are unchanged:
`PreviewCapability.CheckedAt` is a pointer (nil omits; a nonnil value marshals
as given), and `CronPreview.Next` is a slice (empty omits; defined activations
retain their encoding). Their TypeScript mirrors are respectively optional
string and string array. A nonnil pointer to zero is not rewritten globally.

Required timestamps, including partial `AgentSession.StartedAt/UpdatedAt`,
`TerminalSession.CreatedAt`, `SearchHit.At`, event envelopes and notification
times, keep their existing field names, types and encoding. Partial imported
sessions can have unavailable required times. The browser display boundary
guards those legacy zero values rather than inventing an activity time.
Other required timestamps are not assumed broken. Producer source was inspected;
the tests use synthetic values, not real credentials, devices or providers.

## Browser display policy

`web/src/lib/format.ts` is the existing shared relative-time display boundary.
`normalizeTimestamp` accepts a zoned RFC 3339 string with a valid calendar date
and at most nine fractional digits. It returns the original string unchanged.
Missing, null, empty, invalid input and the exact Go zero instant (including
equivalent offsets and all-zero fractions) return undefined. Other defined
year-one instants are preserved; the year alone is not an absence marker.

`ago` displays unavailable input or a future instant as `—`, including positive
sub-millisecond clock skew. It does not clamp future times to `now`, substitute
the current time, or manufacture an elapsed value. Future times such as quota
resets and scheduled activations remain valid data; they simply have no past
age. Invalid comparison clocks also display `—`.

Known past values keep existing relative and local-calendar formatting: elapsed
seconds/minutes/hours first, then Yesterday, weekday and date. UTC offsets
determine the instant. DST can produce 23/25-hour days or repeated wall-clock
hours; these rules remain unchanged. JavaScript Date displays at millisecond
resolution; the original offset and finer precision remain intact in data.
The existing duration formatter already rejects negative/nonfinite durations.

Current production consumers are command search's `resultItem` accessory and
the notification inbox's `<time>` text. An absent search time has no accessory;
a truthy legacy/invalid/future time displays `—`. The inbox displays `—` for
unknown time. Valid `<time datetime>` values remain unchanged. Feature views
that have not yet been implemented do not establish runtime display proof.
The generic live-event decoder and unknown-event handling are unchanged.

## Compatibility

Current browser optionality already accepts omitted fields. Third-party callers
must treat missing fields as unknown rather than assuming every object has a
timestamp. Fresh Go decoding accepts missing, null and legacy year-one values
as zero and re-encodes them absent for these optional fields. Existing decoding
semantics for updates to populated values are unchanged; this is not a new
null-as-clear protocol. Invalid wire strings still fail Go timestamp decoding.
No persistent-state migration is required.

REL-031 deliberately pinned running `FileJob.endedAt` to its then-current
year-one encoding. REL-032 explicitly supersedes that one fixture value with
an omitted field; completed job times and other job/event fields retain their
contracts. The accepted REL-031 snapshot and prior fixture are preserved in
the private completion evidence and starting snapshot. Its API/architecture
notes describing the old zero encoding are historical baseline observations;
this dated migration documents the current contract within REL-032 ownership.
The unchanged mock producer may still emit its legacy running-job sentinel;
the new browser policy tolerates it. Deploy the server and browser together
when available; new browsers tolerate old-server sentinel data. Older clients
must already support optional fields, as declared in the contract.

## Evidence boundary

`testdata/timestamps.json` contains complete synthetic absent/defined responses
for all fourteen fields. Go checks cover missing/null/zero/equivalent-zero,
valid offset/nanoseconds, future values, invalid decoding, embedded contracts,
required partial sessions and the unchanged pointer/slice controls. Daemon
snapshot/event and agent parser checks exercise in-memory production boundaries.
Browser fixtures live in the owned `web/src/api/timestamps.test.tsx` path and
cover display states, offsets, clock skew, invalid input, both DST transitions,
command search and rendered inbox text. Existing event and mock contract tests
cover generic decoding and unknown events. These checks prove only the exercised
source and synthetic browser states, not installed providers, hosted services,
devices, native transcripts or an end-to-end authenticated deployment.
