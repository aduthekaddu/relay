#!/usr/bin/env bash
# id: just
# name: just
# category: cli
# description: A handy command runner for project-specific tasks.
# homepage: https://just.systems
# check: just
# version: just --version
# requires-sudo: false
# platforms: linux, darwin
# size: 5 MB
# tags: tasks
if [ "$(pm)" = brew ]; then
  pkg brew=just
else
  say "Installing just with the official installer"
  run mkdir -p "$BIN_DIR"
  pipe_sh https://just.systems/install.sh bash --to "$BIN_DIR"
fi
ok "just installed"
