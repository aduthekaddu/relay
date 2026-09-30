#!/usr/bin/env bash
# id: zoxide
# name: zoxide
# category: cli
# description: A smarter cd that learns your habits.
# homepage: https://github.com/ajeetdsouza/zoxide
# check: zoxide
# version: zoxide --version
# requires-sudo: false
# platforms: linux, darwin
# size: 2 MB
# tags: cd
if [ "$(pm)" = brew ]; then
  pkg brew=zoxide
else
  say "Installing zoxide with the official installer"
  pipe_sh https://raw.githubusercontent.com/ajeetdsouza/zoxide/main/install.sh sh
fi
ok "zoxide installed — add 'eval \"\$(zoxide init bash)\"' to your shell rc"
