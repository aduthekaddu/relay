# Relay toolbox helpers. Prepended to every recipe at install time; not a
# recipe itself (files starting with "_" are skipped by the catalog).
#
# Environment:
#   RELAY_DRY_RUN=1    print every side-effecting command instead of running it
#   RELAY_OS / RELAY_ARCH / RELAY_PM   override detection (tests)
#   RELAY_BIN_DIR      where user-level binaries are linked (~/.local/bin)
#   RELAY_TOOLS_DIR    where tarball installs live (~/.local/share/relay/tools)
set -euo pipefail

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  _c_step=$'\033[1;35m' _c_ok=$'\033[1;32m' _c_warn=$'\033[1;33m' _c_err=$'\033[1;31m' _c_dim=$'\033[2m' _c_off=$'\033[0m'
else
  _c_step='' _c_ok='' _c_warn='' _c_err='' _c_dim='' _c_off=''
fi

say()  { printf '%s==>%s %s\n' "$_c_step" "$_c_off" "$*"; }
ok()   { printf '%s✓%s %s\n' "$_c_ok" "$_c_off" "$*"; }
warn() { printf '%s!%s %s\n' "$_c_warn" "$_c_off" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$_c_err" "$_c_off" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
dry()  { [ -n "${RELAY_DRY_RUN:-}" ]; }

_detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *) echo unknown ;;
  esac
}
_detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) echo unknown ;;
  esac
}
RELAY_OS="${RELAY_OS:-$(_detect_os)}"
RELAY_ARCH="${RELAY_ARCH:-$(_detect_arch)}"
BIN_DIR="${RELAY_BIN_DIR:-$HOME/.local/bin}"
TOOLS_DIR="${RELAY_TOOLS_DIR:-$HOME/.local/share/relay/tools}"
export PATH="$BIN_DIR:$PATH"

# run CMD... — execute (or print in dry-run mode) one command.
run() {
  if dry; then
    printf '%s+%s' "$_c_dim" "$_c_off"
    printf ' %q' "$@"
    printf '\n'
  else
    "$@"
  fi
}

# as_root CMD... — run with root privileges (sudo prompts in this terminal).
as_root() {
  if [ "$(id -u)" = 0 ]; then
    run "$@"
  elif have sudo || dry; then
    run sudo "$@"
  else
    die "this step needs root: install sudo or run the recipe as root"
  fi
}

# need BIN [HINT] — require a command (only warns in dry-run mode).
need() {
  have "$1" && return 0
  if dry; then
    warn "(dry-run) would need '$1'"
    return 0
  fi
  die "'$1' is required${2:+: $2}"
}

# pm — the package manager to use: brew, apt, dnf, pacman, zypper or apk.
pm() {
  if [ -n "${RELAY_PM:-}" ]; then echo "$RELAY_PM"; return; fi
  if [ "$RELAY_OS" = darwin ]; then echo brew; return; fi
  for m in apt-get:apt dnf:dnf pacman:pacman zypper:zypper apk:apk brew:brew; do
    if have "${m%%:*}"; then echo "${m#*:}"; return; fi
  done
  echo none
}

_apt_updated=''
# pkg apt="a b" dnf="a" pacman="a" zypper="a" apk="a" brew="a" — install
# the names listed for the detected package manager.
pkg() {
  local mgr names='' arg
  mgr="$(pm)"
  for arg in "$@"; do
    case "$arg" in "$mgr="*) names="${arg#*=}" ;; esac
  done
  [ -n "$names" ] || die "no package known for package manager '$mgr' — see the tool's homepage"
  # shellcheck disable=SC2086 # word splitting of package lists is intended
  case "$mgr" in
    apt)
      if [ -z "$_apt_updated" ]; then as_root apt-get update -q; _apt_updated=1; fi
      as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -q $names ;;
    dnf) as_root dnf install -y $names ;;
    pacman) as_root pacman -S --needed --noconfirm $names ;;
    zypper) as_root zypper --non-interactive install $names ;;
    apk) as_root apk add $names ;;
    brew) need brew "install Homebrew from https://brew.sh"; run brew install $names ;;
    *) die "no supported package manager found" ;;
  esac
}

# fetch URL [PLACEHOLDER] — print a URL's body (the placeholder in dry-run).
fetch() {
  if dry; then printf '%s\n' "${2:-dry-run}"; return; fi
  curl -fsSL --proto '=https' --tlsv1.2 "$1"
}

# download URL DEST — save a URL to a file.
download() {
  if dry; then printf '+ curl -fsSL -o %q %q\n' "$2" "$1"; return; fi
  curl -fL --proto '=https' --tlsv1.2 --progress-bar -o "$2" "$1"
}

# pipe_sh URL SHELL [ARGS...] — run an official installer script. The
# script is downloaded to a file first, so a dropped connection never
# executes half a script.
pipe_sh() {
  local url="$1" shell="$2" tmp
  shift 2
  if dry; then
    printf '+ curl -fsSL %q | %s -s --' "$url" "$shell"
    printf ' %q' "$@"
    printf '\n'
    return
  fi
  tmp="$(mktemp)"
  curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp" "$url"
  "$shell" "$tmp" "$@"
  rm -f "$tmp"
}

# sha256_of FILE — print a file's SHA-256.
sha256_of() {
  if have sha256sum; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# gh_latest OWNER/REPO — print the tag of the latest GitHub release.
gh_latest() {
  if dry; then echo v0.0.0; return; fi
  local url
  url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$1/releases/latest")"
  echo "${url##*/}"
}

# link_bin TARGET [NAME] — symlink an executable into BIN_DIR.
link_bin() {
  run mkdir -p "$BIN_DIR"
  run ln -sfn "$1" "$BIN_DIR/${2:-$(basename "$1")}"
}

# npm_global PACKAGE — install a global npm package, into ~/.local when
# the global prefix is not writable (so no sudo is ever needed).
npm_global() {
  need npm "install Node.js first (Toolbox → Node.js)"
  if dry || [ -w "$(npm prefix -g 2>/dev/null || echo /nonexistent)" ]; then
    run npm install -g "$1"
  else
    run npm install -g --prefix "$HOME/.local" "$1"
  fi
}

# sudo_write FILE — write stdin to a root-owned file.
sudo_write() {
  if dry; then printf '+ sudo tee %q <<EOF\n' "$1"; cat; echo EOF; return; fi
  if [ "$(id -u)" = 0 ]; then tee "$1" >/dev/null; else sudo tee "$1" >/dev/null; fi
}

# os_id — the ID field of /etc/os-release (ubuntu, debian, fedora, …).
os_id() {
  if [ -r /etc/os-release ]; then (. /etc/os-release && echo "${ID:-unknown}"); else echo unknown; fi
}

# platform_die — stop with a clear message on an unsupported platform.
platform_die() { die "not supported on $RELAY_OS/$RELAY_ARCH"; }
