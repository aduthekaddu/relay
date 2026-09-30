#!/usr/bin/env bash
# id: node
# name: Node.js (LTS)
# category: runtimes
# description: Latest Node.js LTS from the official tarball (checksum verified), npm included.
# homepage: https://nodejs.org
# check: node, npm
# version: node --version
# requires-sudo: false
# platforms: linux, darwin
# size: 50 MB
# tags: javascript, npm
case "$RELAY_ARCH" in amd64) narch=x64 ;; arm64) narch=arm64 ;; *) platform_die ;; esac
say "Looking up the current Node.js LTS"
lts="$(fetch https://nodejs.org/dist/index.tab "$(printf 'version\tdate\tfiles\tnpm\tv8\tuv\tzlib\topenssl\tmodules\tlts\tsecurity\nv24.0.0\t-\t-\t-\t-\t-\t-\t-\t-\tKrypton\t-')" |
  awk -F'\t' 'NR > 1 && $10 != "-" { print $1; exit }')"
[ -n "$lts" ] || die "could not determine the Node.js LTS version"
name="node-$lts-$RELAY_OS-$narch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
say "Downloading $name"
download "https://nodejs.org/dist/$lts/$name.tar.gz" "$tmp/$name.tar.gz"
download "https://nodejs.org/dist/$lts/SHASUMS256.txt" "$tmp/SHASUMS256.txt"
if ! dry; then
  want="$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/SHASUMS256.txt")"
  got="$(sha256_of "$tmp/$name.tar.gz")"
  [ -n "$want" ] && [ "$want" = "$got" ] || die "checksum mismatch for $name.tar.gz"
  ok "checksum verified"
fi
run mkdir -p "$TOOLS_DIR"
run rm -rf "${TOOLS_DIR:?}/$name"
run tar -xzf "$tmp/$name.tar.gz" -C "$TOOLS_DIR"
run ln -sfn "$TOOLS_DIR/$name" "$TOOLS_DIR/node"
for b in node npm npx corepack; do link_bin "$TOOLS_DIR/node/bin/$b"; done
ok "Node.js $lts installed to $TOOLS_DIR/node (linked into $BIN_DIR)"
