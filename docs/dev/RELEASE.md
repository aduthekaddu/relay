# Releasing Relay

A release is a git tag. Pushing `vX.Y.Z` runs `.github/workflows/release.yml`,
which publishes everything `scripts/install.sh` and `relay update` need.

## Cut a release

1. `main` is green in CI (`.github/workflows/ci.yml`: gofmt, `go vet`,
   `go test -race`, CGO-free build, `bash -n scripts/install.sh`; web
   typecheck, biome, vitest, build; site build when `site/` exists).
2. Tag and push:

   ```bash
   git switch main && git pull --ff-only
   git tag -a v0.3.0 -m "Relay v0.3.0"
   git push origin v0.3.0
   ```

   A tag with a hyphen (`v0.3.0-rc.1`) becomes a GitHub pre-release, which
   `releases/latest` — and therefore the installer and `relay update` without
   `--version` — ignores.
3. Watch the `release` workflow. When it finishes the release page has notes
   generated from merged PRs since the previous tag; edit them if needed.

## What the workflow does

| job | work |
| --- | --- |
| `web` | `pnpm install --frozen-lockfile && pnpm build` → `internal/web/dist`, uploaded once |
| `build` | matrix `linux`/`darwin` × `amd64`/`arm64`; `go test` on linux/amd64; `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X …/internal/version.Version=<tag> -X ….Commit=<sha> -X ….Date=<utc>"` |
| `publish` | `sha256sum relay_* > checksums.txt`; `gh release create <tag> --generate-notes --verify-tag` with the four binaries and `checksums.txt` |

Asset names are a contract: `relay_<os>_<arch>` (no archive, directly
executable) and `checksums.txt` in `sha256sum` format. Changing them breaks
every installed `relay update` and the one-line installer.

## How users receive it

- New installs: `curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash`
  downloads `releases/latest/download/relay_<os>_<arch>` and `checksums.txt`,
  refuses to install on a checksum mismatch, installs to `~/.local/bin/relay`
  (or `$RELAY_INSTALL_DIR`) and runs `relay setup`. Re-running it upgrades in
  place and keeps `relay.toml`.
- Existing installs: `relay update` (or `--check`, `--version vX.Y.Z`,
  `--force`) reads the GitHub releases API, verifies the checksum, replaces
  the binary atomically (rename over the running file) and restarts
  `relay.service` only. `relay-ptyd` keeps running, so terminal sessions
  survive an update; `--all` restarts it too, `--no-restart` restarts nothing.

## Local and test builds

```bash
make release            # web + dist/relay_{linux,darwin}_{amd64,arm64} + checksums.txt
```

Mirrors and tests can point the installer and updater elsewhere:

- `RELAY_DOWNLOAD_URL=<dir>` — installer: a directory holding
  `relay_<os>_<arch>` and `checksums.txt` (e.g. `python3 -m http.server`).
- `RELAY_REPO=owner/name` — installer: a fork's releases.
- `RELAY_RELEASES_API=<url>` — `relay update`: a releases API base.

### Installer smoke test in a throwaway container

```bash
mkdir -p /tmp/relay-e2e/dl && cp scripts/install.sh /tmp/relay-e2e/
CGO_ENABLED=0 GOARCH=$(go env GOARCH) go build -o /tmp/relay-e2e/dl/relay_linux_$(go env GOARCH) ./cmd/relay
(cd /tmp/relay-e2e/dl && sha256sum relay_* > checksums.txt && python3 -m http.server 47708 --bind 127.0.0.1) &
docker run --rm --network host -v /tmp/relay-e2e:/e2e:ro ubuntu:24.04 bash -c '
  apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
  printf "a-long-test-password\n" | RELAY_DOWNLOAD_URL=http://127.0.0.1:47708 bash /e2e/install.sh \
    --yes --no-systemd --units-dir /tmp/units --access local --user alice --password-stdin
  ~/.local/bin/relay doctor'
```

Stop the HTTP server afterwards (kill its PID).

## Versioning

Semantic versioning. Until 1.0, minor versions may change config keys; every
such change needs a migration or a `relay doctor` hint, and a line in the
release notes. The `relay.toml` format and the ptyd protocol must stay
compatible across a `relay update` that does not use `--all`, because the
old ptyd keeps running with the new server.
