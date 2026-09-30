#!/usr/bin/env bash
# Relay installer.
#
#   curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
#   curl -fsSL …/install.sh | bash -s -- --version v0.3.0 --access sslip
#
# Downloads the release binary for this OS/arch, verifies its sha256
# against the release's checksums.txt, installs it to ~/.local/bin/relay
# and runs `relay setup`. Re-running upgrades in place; your relay.toml,
# data and running terminals are kept.
#
# Options (everything else is passed on to `relay setup`):
#   --version vX.Y.Z   install this release (default: latest)
#   --dir DIR          install directory (default: $RELAY_INSTALL_DIR or ~/.local/bin)
#   --from-source      build from a git checkout (needs git, go, node and pnpm)
#   --no-setup         only install the binary
#   --yes              non-interactive (also passed to setup)
#   -h, --help         this help
#
# Environment:
#   RELAY_VERSION, RELAY_INSTALL_DIR, RELAY_NO_SETUP=1
#   RELAY_REPO          GitHub owner/name (default aduthekaddu/relay)
#   RELAY_DOWNLOAD_URL  directory holding relay_<os>_<arch> and checksums.txt
#                       (overrides GitHub; for mirrors and tests)
#   NO_COLOR            disable colours
set -euo pipefail

# Everything runs inside main so a truncated download cannot execute a
# partial script.
main() {
  local repo="${RELAY_REPO:-aduthekaddu/relay}"
  local version="${RELAY_VERSION:-}"
  local dir="${RELAY_INSTALL_DIR:-$HOME/.local/bin}"
  local from_source="" no_setup="${RELAY_NO_SETUP:-}" yes=""
  local -a setup_args=()

  setup_colors
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) need_value "$@"; version="$2"; shift 2 ;;
      --version=*) version="${1#*=}"; shift ;;
      --dir) need_value "$@"; dir="$2"; shift 2 ;;
      --dir=*) dir="${1#*=}"; shift ;;
      --from-source) from_source=1; shift ;;
      --no-setup) no_setup=1; shift ;;
      --yes|-y) yes=1; setup_args+=(--yes); shift ;;
      -h|--help) usage; exit 0 ;;
      --) shift; setup_args+=("$@"); break ;;
      *) setup_args+=("$1"); shift ;;
    esac
  done
  if [ -n "$version" ] && [[ "$version" != v* ]]; then version="v$version"; fi
  if [ -n "$version" ] && ! [[ "$version" =~ ^v[0-9A-Za-z.+-]+$ ]]; then
    die "invalid version '$version'"
  fi

  printf '\n  %s %s\n  %s\n\n' "$(c_accent '▲')" "$(c_bold 'Relay installer')" "$(c_dim 'Your machine, from any browser.')"

  local os arch
  os="$(detect_os)"
  arch="$(detect_arch)"
  info "Platform: ${os}/${arch}"

  local tmp
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/relay-install.XXXXXX")"
  # shellcheck disable=SC2064 # expand now: tmp is local
  trap "rm -rf '$tmp'" EXIT

  local previous=""
  if [ -x "$dir/relay" ]; then
    previous="$("$dir/relay" version 2>/dev/null | awk '{print $2}' || true)"
  fi

  if [ -n "$from_source" ]; then
    build_from_source "$repo" "$version" "$tmp"
  else
    download_release "$repo" "$version" "$os" "$arch" "$tmp"
  fi

  mkdir -p "$dir"
  # Atomic replace: a running `relay serve` keeps the old inode.
  install -m 0755 "$tmp/relay" "$dir/.relay.new"
  mv -f "$dir/.relay.new" "$dir/relay"
  local now
  now="$("$dir/relay" version 2>/dev/null | awk '{print $2}' || true)"
  if [ -n "$previous" ] && [ "$previous" != "$now" ]; then
    ok "Upgraded relay ${previous} → ${now} in $dir"
  elif [ -n "$previous" ]; then
    ok "Reinstalled relay ${now} in $dir"
  else
    ok "Installed relay ${now} to $dir/relay"
  fi

  path_hint "$dir"

  if [ -n "$no_setup" ]; then
    restart_if_running
    info "Next: run $(c_code 'relay setup') to configure access and start Relay."
    return 0
  fi
  run_setup "$dir/relay" "$yes" "${setup_args[@]+"${setup_args[@]}"}"
}

usage() {
  cat <<'EOF_USAGE'
Relay installer

  curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash
  curl -fsSL .../install.sh | bash -s -- [options] [relay setup flags]

Options:
  --version vX.Y.Z   install this release (default: latest)
  --dir DIR          install directory (default: $RELAY_INSTALL_DIR or ~/.local/bin)
  --from-source      build from a git checkout (needs git, go, node, pnpm, make)
  --no-setup         only install the binary
  --yes              non-interactive (also passed to relay setup)
  -h, --help         this help

Any other flag goes to `relay setup`, e.g. --access sslip, --components agents,tools.
Environment: RELAY_VERSION, RELAY_INSTALL_DIR, RELAY_NO_SETUP=1, RELAY_REPO,
RELAY_DOWNLOAD_URL (directory with relay_<os>_<arch> + checksums.txt), NO_COLOR.
EOF_USAGE
}

need_value() {
  if [ $# -lt 2 ] || [[ "$2" == --* ]]; then die "$1 needs a value"; fi
}

# ---- output ---------------------------------------------------------------

setup_colors() {
  if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != "dumb" ]; then
    C_RESET=$'\033[0m' C_BOLD=$'\033[1m' C_DIM=$'\033[2m' C_ACCENT=$'\033[1;38;5;141m'
    C_OK=$'\033[1;32m' C_WARN=$'\033[1;33m' C_ERR=$'\033[1;31m' C_CODE=$'\033[36m'
  else
    C_RESET="" C_BOLD="" C_DIM="" C_ACCENT="" C_OK="" C_WARN="" C_ERR="" C_CODE=""
  fi
}
c_bold()   { printf '%s%s%s' "$C_BOLD" "$1" "$C_RESET"; }
c_dim()    { printf '%s%s%s' "$C_DIM" "$1" "$C_RESET"; }
c_accent() { printf '%s%s%s' "$C_ACCENT" "$1" "$C_RESET"; }
c_code()   { printf '%s%s%s' "$C_CODE" "$1" "$C_RESET"; }
info() { printf '  %s\n' "$*"; }
ok()   { printf '  %s✓%s %s\n' "$C_OK" "$C_RESET" "$*"; }
warn() { printf '  %s!%s %s\n' "$C_WARN" "$C_RESET" "$*" >&2; }
die()  { printf '  %s✗%s %s\n' "$C_ERR" "$C_RESET" "$*" >&2; exit 1; }

# ---- platform -------------------------------------------------------------

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *) die "unsupported OS $(uname -s): Relay runs on Linux and macOS" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported CPU $(uname -m): release builds exist for amd64 and arm64 (try --from-source)" ;;
  esac
}

# ---- download -------------------------------------------------------------

fetch() { # fetch URL OUT
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --proto '=https,http' --retry 3 --connect-timeout 15 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q --tries=3 --timeout=30 -O "$2" "$1"
  else
    die "need curl or wget to download Relay"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

download_release() { # repo version os arch tmp
  local repo="$1" version="$2" asset="relay_$3_$4" tmp="$5" base
  if [ -n "${RELAY_DOWNLOAD_URL:-}" ]; then
    base="${RELAY_DOWNLOAD_URL%/}"
  elif [ -n "$version" ]; then
    base="https://github.com/${repo}/releases/download/${version}"
  else
    base="https://github.com/${repo}/releases/latest/download"
  fi
  info "Downloading $asset ${version:-(latest)}…"
  fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "could not download $base/checksums.txt"
  fetch "$base/$asset" "$tmp/relay" || die "could not download $base/$asset"
  local want got
  want="$(awk -v a="$asset" '{n=$2; sub(/^\*/, "", n)} n==a {print tolower($1); exit}' "$tmp/checksums.txt")"
  [ -n "$want" ] || die "checksums.txt has no entry for $asset"
  got="$(sha256_of "$tmp/relay")"
  [ "$got" = "$want" ] || die "checksum mismatch for $asset (got $got, want $want) — not installing"
  ok "Checksum verified (sha256 ${got:0:12}…)"
  chmod 0755 "$tmp/relay"
}

build_from_source() { # repo version tmp
  local repo="$1" version="$2" tmp="$3" tool
  for tool in git go node pnpm make; do
    command -v "$tool" >/dev/null 2>&1 || die "--from-source needs $tool on PATH"
  done
  local src="$tmp/src"
  info "Cloning https://github.com/${repo} ${version:-(main)}…"
  if [ -n "$version" ]; then
    git clone --quiet --depth 1 --branch "$version" "https://github.com/${repo}.git" "$src"
  else
    git clone --quiet --depth 1 "https://github.com/${repo}.git" "$src"
  fi
  info "Building (web app + Go binary; this takes a few minutes)…"
  (cd "$src/web" && pnpm install --frozen-lockfile --silent)
  make -C "$src" build
  [ -x "$src/bin/relay" ] || die "build finished without bin/relay"
  cp "$src/bin/relay" "$tmp/relay"
  ok "Built $("$tmp/relay" version | awk '{print $2}')"
}

# ---- after install --------------------------------------------------------

path_hint() {
  local dir="$1"
  case ":$PATH:" in
    *":$dir:"*) return 0 ;;
  esac
  local rc="~/.profile"
  case "${SHELL:-}" in
    */zsh) rc="~/.zshrc" ;;
    */bash) rc="~/.bashrc" ;;
    */fish) warn "$dir is not on your PATH. Add it with: fish_add_path $dir"; return 0 ;;
  esac
  warn "$dir is not on your PATH. Add it with:"
  info "  $(c_code "echo 'export PATH=\"$dir:\$PATH\"' >> $rc")"
}

restart_if_running() {
  if command -v systemctl >/dev/null 2>&1 && systemctl --user is-active --quiet relay.service 2>/dev/null; then
    systemctl --user restart relay.service && ok "Restarted relay.service (terminal sessions keep running)"
  fi
}

have_tty() { (exec </dev/tty) 2>/dev/null; }

run_setup() { # bin yes args…
  local bin="$1" yes="$2"
  shift 2
  if [ -z "$yes" ] && [ ! -t 0 ] && ! have_tty; then
    warn "No terminal for the setup questions."
    info "Finish with $(c_code "$bin setup"), or re-run this installer with --yes for defaults."
    return 0
  fi
  printf '\n'
  if [ -t 0 ] || [ -n "$yes" ]; then
    "$bin" setup "$@"
  else
    # curl | bash: stdin is the script, so answer prompts from the terminal.
    "$bin" setup "$@" </dev/tty
  fi
}

main "$@"
