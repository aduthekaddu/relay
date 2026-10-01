# Files and system

Owners: `internal/files`, `internal/system`, `relay open`
(`internal/cli/cmd_open.go`). The endpoint contracts are in `API.md` under
"files" and "system"; extra types are in `internal/api/files.go`, mirrored in
`web/src/api/files.ts`. This document covers behaviour and limits.

## Files

### Path safety (`files.Resolver`)

Every client path goes through one resolver bound to `files.root` (default:
home):

1. `~` and `~/…` expand to home. Relative paths resolve against the root.
2. `filepath.Clean`, then a lexical check that the result is the root or
   below it. `..` escapes and absolute paths outside the root return
   **403 `forbidden`**. The response is the same whether or not the target
   exists, so errors never reveal what is outside the root.
3. `filepath.EvalSymlinks`, then the same check on the real path. A symlink
   inside the root that points outside it is refused.
4. Reads open with `O_NOFOLLOW|O_NONBLOCK` and check the opened file is the
   same regular file that was checked. Devices, sockets and FIFOs are
   refused with 403.
5. Rename, move and delete act on the entry itself (`ResolveEntry`: the
   parent is resolved and the final component is kept), so they affect a
   symlink, never its target. The root itself cannot be renamed, moved or
   deleted.

Names supplied for `rename`/`mkdir`/`touch` are single components: no `/`,
NUL, `.` or `..`, at most 255 bytes.

### Listing

`GET /files/list` does one `ReadDir` plus one `Lstat` per entry. It sorts by
`name` (directories first, natural order), `size` or `mtime` (with `desc`), and
pages with `offset`/`limit` (default 500, max 5000). The response also counts
hidden entries: dotfiles are hidden unless `hidden=1` or
`files.show_hidden` is set. Directories on the page get a child count, capped
at 1000 entries per directory and computed for at most 500 directories per
page. When the directory is inside a git repository, each entry gets its git
status letter. That comes from one `git status --porcelain=v1 -z` per
repository, with a 2 s timeout and an 8 MiB output cap, cached for 5 s.

### Raw, text and thumbnails

- **Raw** is served with `http.ServeContent`, so `Range` and conditional
  requests work. The type comes from the extension, or from sniffing when
  there is no extension. Every response carries `Content-Security-Policy:
  sandbox; default-src 'none'; …` and `X-Content-Type-Options: nosniff`.
  Scriptable types (HTML, SVG, XML, JS, PDF…) are always sent as
  `attachment`, including a body that sniffs as HTML under a harmless
  extension. `download=1` forces `attachment` for any type. Images, audio,
  video and plain text are sent inline. Other text types are sent as
  `text/plain`.
- **Text**: the limit is 5 MiB. The encoding is `utf-8`, `utf-8-bom` (the BOM
  is stripped on read and restored on save) or `binary`. Binary means a NUL
  byte or invalid UTF-8; its `text` is empty.
  `PUT /files/text?path=&mtime=` saves atomically (temporary file, then
  rename, keeping the file mode). Files with several hard links are
  rewritten in place, so the links stay shared. If the file's modification time no longer
  matches `mtime`, the save returns **409**. Overwriting an existing file
  publishes a `file.overwrite` audit event.
- **Thumbnails**: JPEG, PNG, GIF and WebP (`x/image/webp`) sources are
  supported. Sources above 40 MP or 64 MiB are refused. The downscale is an
  area-average box filter written for this package. It works one row at a
  time in premultiplied alpha and never upscales. JPEG EXIF orientation is
  applied. Requested sizes are rounded up to fixed buckets. Output is JPEG,
  or PNG when the source has alpha. Thumbnails are cached in
  `CacheDir/thumbs`, keyed by a hash of path, mtime, size and bucket, and
  concurrent requests for the same key are coalesced. The cache is pruned
  daily: entries unused for 30 days go first, then the least recently used
  until it is under 512 MiB.

### Operations

- `mkdir` creates missing parents. `touch` creates a file or updates its
  mtime. `rename` never replaces an existing entry (`renameat2
  RENAME_NOREPLACE` on Linux, with a check-then-rename fallback elsewhere).
- `move` accepts up to 1000 sources. It uses `rename` within a filesystem
  and copy-then-remove across filesystems. It refuses to move a folder into
  itself, and name clashes return 409.
- `copy` runs as a background job and returns **202** with an `api.FileJob`.
  The job counts files and bytes first (for at most 10 s), then copies,
  publishing `files.job` events at most about 4 per second and once at the
  end. Symlinks are recreated as links, never followed. Unreadable and
  special files are skipped and counted in `skipped`. `GET /files/jobs` lists
  jobs and `DELETE /files/jobs/{id}` cancels one. At most 4 jobs run at once,
  and finished jobs stay listed for 10 minutes.
- `delete` moves items to the trash when `trash` is set (the default follows
  `files.use_trash`). Otherwise, or for items already in the trash, it
  deletes them permanently. It publishes a `file.trash` or `file.delete`
  audit event.

### Trash (freedesktop.org Trash 1.0)

The trash is the home trash, `$XDG_DATA_HOME/Trash`
(`~/.local/share/Trash`), with `files/` and `info/<name>.trashinfo`
(`Path` percent-encoded, `DeletionDate` in local time). The info file is
created first with `O_EXCL`, which reserves the name. Clashes become
`name (2).ext`. Items on another filesystem than the trash get
`ErrCrossDevice`, and the client can offer a permanent delete instead.
`GET /files/trash` lists the trash, newest first. `restore` puts an item back
at its original path, renamed if that path is taken, and recreates missing
parents. `trash/empty` publishes a `file.trash.empty` audit event. Symlinks inside
the trash are never followed.

### Zip, usage and search

- **Zip** (`GET /files/zip?paths=…`) streams the archive as it is built.
  Entries are deflated, except already-compressed formats, which are
  stored. Unreadable entries are skipped. A symlink is included only when
  its target is inside the root; a directory target is not walked, to avoid
  loops.
- **Usage** (`GET /files/usage?path=`) returns `pending:true` straight away
  and scans in the background. At most 2 scans run at once. A scan stays on
  one filesystem, never follows symlinks, counts hard links once, and stops
  after 3 minutes or 5 M entries (`complete:false`). Results are cached for
  10 minutes; `refresh=1` rescans. The 300 largest children are listed and
  the rest are summed under `…`.
- **Search** (`GET /files/search`) streams NDJSON `FileSearchHit` lines.
  - Name search walks from `path` and skips `node_modules`, `.git`, `vendor`
    and `.cache`. It stops at `limit` (at most 500) or after 3 s.
  - Content search (`content=1`) runs `rg --json --fixed-strings
    --smart-case -- <q> <dir>` when ripgrep is installed. The query is passed
    as an argv element after `--` and is never a shell string. Without
    ripgrep, a bounded Go scan reads files up to 1 MiB. Content search stops
    after 10 s.
  - At most 2 searches run at once.
- **Command-center provider** (scope `files`): an in-memory name index of
  workspace roots (depth 6) and home (depth 3), capped at 50 000 entries. It
  is rebuilt every 5 minutes, so queries never touch the disk.

### `relay open` / `POST /api/v1/open`

`relay open <path[:line]>` resolves the path against the current directory
and calls `POST /api/v1/open` over the local control socket. The server
validates the path with the resolver and publishes `api.EvOpen`
(`{path, line?}`). Browsers open the file in the editor, or in the file
manager for folders. The CLI exits with 1 and prints the server's error when
the path is outside the root.

## System

### Metrics (`internal/system`, Linux)

Everything comes from `/proc` and `/sys`. The roots are injectable so tests
run against fixture trees.

| Field | Source |
| --- | --- |
| CPU total and per-core % | deltas of `/proc/stat` (idle = idle + iowait; guest time excluded) |
| CPU model | `/proc/cpuinfo` `model name`, `Hardware`, or the arm64 implementer/part table |
| Temperature | best `/sys/class/thermal/thermal_zone*` (`x86_pkg_temp` or cpu first, then `acpitz`) |
| Memory and swap | `/proc/meminfo` (used = total − available; cached = Cached + SReclaimable + Buffers) |
| Disks | `/proc/self/mounts`: `/dev/*` sources plus nfs, cifs, zfs, fuseblk, virtiofs…; skips loop, squashfs, tmpfs, overlay, `/snap`, docker and `/run` (except `/run/media`); one entry per device (shortest mount); `statfs` for sizes; `/proc/diskstats` for read/write B/s |
| Network | `/proc/net/dev`, excluding `lo`; interfaces with no `/sys/class/net/<if>/device` (docker, veth, bridges, VPNs) are virtual and left out of the totals unless no physical NIC exists |
| Load, uptime, procs | `/proc/loadavg`, `/proc/uptime`, numeric entries in `/proc` |
| GPU | `nvidia-smi --query-gpu=… --format=csv,noheader,nounits` when installed; 2 s timeout; run at most every 5 s |
| Host | hostname, `os-release` `PRETTY_NAME`, `/proc/sys/kernel/osrelease`, `GOARCH`, `systemd-detect-virt` (probed once) |

Sampling runs in `Service.Start`:

- **1 Hz** while at least one visible browser is subscribed to `metrics`.
  Each sample is published as `api.EvMetrics`. Hidden tabs do not keep this
  high-frequency sampler active or receive metrics frames.
- Otherwise **every 10 s**, only to feed the history ring: 360 samples (one
  hour), with the host and CPU model left out.

Sampling is serialised, and a sample younger than 0.9 s is reused, so rates
never come from tiny windows. `GET /system/metrics` returns a sample at most
2 s old (the first ever call primes counters for 250 ms).
`GET /system/metrics/history?minutes=1..60` returns ring samples, oldest
first.

On macOS and other non-Linux systems (`platform_other.go`), a fallback
reports core count, `hw.memsize`, load, uptime, the root and home
filesystems and host identity from `sysctl`. Rates are zero, and processes
come from `ps`.

### Processes

`GET /system/processes?sort=cpu|mem|pid|name&limit=&q=` reads
`/proc/<pid>/{stat,cmdline}` and the directory owner (the uid). User names
come from `/etc/passwd`, cached for 1 minute. The command line is cut at 512
bytes.

- **CPU %** is the share of one core since the previous listing. When no
  listing happened in the last 5 s, the call primes counters and waits
  300 ms. A pid reused by a new process is detected by its start time.
- **`terminal`**: the id of the Relay terminal whose shell pid (from
  `d.Pty.List`, 1 s timeout) the process descends from.
- **`protected`**: pid 1, kthreadd and kernel threads, Relay itself, its
  parent, every process running the Relay binary (`serve`, `ptyd`), and
  every process of another user.

`POST /system/processes/{pid}/signal` accepts `TERM`, `KILL`, `INT`, `HUP`,
`STOP`, `CONT`, `QUIT`, `USR1` and `USR2` (with or without `SIG`, or as
numbers). Protected processes get **403**; a missing pid gets 404. Each
signal sent publishes a `process.signal` audit event.

### Services

`GET /system/services` runs `systemctl --user list-units --type=service
--all --no-legend --plain`, then one batched `systemctl --user show -- <units…>`
for restarts and `since`. `since` is computed from monotonic timestamps plus
the boot time, so it does not depend on locale or timezone parsing. Results
are cached for 2 s. They are sorted Relay units first, then failed, then
active, then the rest. `managed` means the name starts with `relay`. Without
systemd the list is empty.

`POST /system/services/{name}/{start|stop|restart}`:

- The name is validated (`[A-Za-z0-9:_.@\-]+.service`, no leading `-`, and
  `.service` is added when missing).
- Only managed units are allowed unless `Service.ManageAllUnits` is set;
  other units get **403**.
- The action has a 30 s timeout and publishes a `service.<action>` audit
  event.

### Logs (WebSocket)

`GET /system/logs?unit=|file=&lines=200` (lines 0–2000) sends one
`api.LogLine` JSON text frame per line.

- `unit`: runs `journalctl --user -u <unit> -f -o json -n N` as argv.
  `MESSAGE` may be a string, null or a byte array; the priority and realtime
  timestamp are kept.
- `file`: the path must resolve (with the files resolver bound to home)
  to a regular file inside home. The stream sends the last N lines (looking
  back at most 1 MiB), then polls every 500 ms like `tail -F`: truncation
  restarts from the beginning, and a rotated file is finished and then
  reopened. Opens use `O_NOFOLLOW|O_NONBLOCK`.

Limits: 8 KiB per line (cut on a rune boundary), invalid UTF-8 replaced, and
ANSI and control characters stripped. At most 2000 lines are read per poll
and at most 8 streams are open at once (429 beyond that). A write that takes
longer than 10 s ends the stream. The server pings every 25 s. When the
client closes the socket, the context is cancelled and `journalctl` is killed
and reaped.

### Search provider

Scope `processes`: the query must be at least 2 characters (or digits for a
pid). It matches name, command line, pid, user or terminal id and returns the
busiest processes first. Each result links to `/system/processes?pid=N`, and
`meta` carries `cpu`, `user`, `terminal` and `protected`.

## Wiring notes

- `wireFiles` and `wireSystem` register routes, `Start` loops and search
  providers.
- The files and system services publish audit entries (`api.AuditEntry`) on
  the bus topic `"audit"` (= `core.BusAudit`) unless their `Audit` hook is
  set. Once `core.AuditEvent` exists, wiring sets the hook to translate.
- `wireSystem` points `system.Service.Subscribed` at the `Presence` installed
  by `wireLive`. The query counts visible subscribed clients; nil means no
  subscriber, so the service keeps only the 10 s history sampler.
