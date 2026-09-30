#!/usr/bin/env bash
# id: neovim
# name: Neovim
# category: cli
# description: Hyperextensible Vim-based editor (latest stable release).
# homepage: https://neovim.io
# check: nvim
# version: nvim --version
# requires-sudo: false
# platforms: linux, darwin
# size: 30 MB
# tags: editor, vim
if [ "$(pm)" = brew ]; then
  pkg brew=neovim
  ok "Neovim installed"
  exit 0
fi
case "$RELAY_ARCH" in amd64) narch=x86_64 ;; arm64) narch=arm64 ;; *) platform_die ;; esac
name="nvim-linux-$narch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
say "Downloading the latest Neovim release"
download "https://github.com/neovim/neovim/releases/latest/download/$name.tar.gz" "$tmp/$name.tar.gz"
run mkdir -p "$TOOLS_DIR"
run rm -rf "${TOOLS_DIR:?}/$name"
run tar -xzf "$tmp/$name.tar.gz" -C "$TOOLS_DIR"
link_bin "$TOOLS_DIR/$name/bin/nvim"
ok "Neovim installed to $TOOLS_DIR/$name"
