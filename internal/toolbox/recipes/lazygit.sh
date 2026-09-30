#!/usr/bin/env bash
# id: lazygit
# name: lazygit
# category: cli
# description: Simple terminal UI for git.
# homepage: https://github.com/jesseduffield/lazygit
# check: lazygit
# version: lazygit --version
# requires-sudo: false
# platforms: linux, darwin
# size: 20 MB
# tags: git
mgr="$(pm)"
if [ "$mgr" = brew ] || [ "$mgr" = pacman ]; then
  pkg brew=lazygit pacman=lazygit
  ok "lazygit installed"
  exit 0
fi
case "$RELAY_ARCH" in amd64) larch=x86_64 ;; arm64) larch=arm64 ;; *) platform_die ;; esac
tag="$(gh_latest jesseduffield/lazygit)"
file="lazygit_${tag#v}_Linux_${larch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
say "Downloading lazygit $tag"
download "https://github.com/jesseduffield/lazygit/releases/download/$tag/$file" "$tmp/$file"
run tar -xzf "$tmp/$file" -C "$tmp" lazygit
run mkdir -p "$BIN_DIR"
run install -m 0755 "$tmp/lazygit" "$BIN_DIR/lazygit"
ok "lazygit $tag installed to $BIN_DIR"
