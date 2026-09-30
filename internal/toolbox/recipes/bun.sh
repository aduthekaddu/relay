#!/usr/bin/env bash
# id: bun
# name: Bun
# category: runtimes
# description: Fast all-in-one JavaScript runtime, bundler and package manager.
# homepage: https://bun.sh
# check: bun
# version: bun --version
# requires-sudo: false
# platforms: linux, darwin
# size: 40 MB
# tags: javascript
if ! have unzip; then
  say "Installing unzip (needed by the Bun installer)"
  pkg apt=unzip dnf=unzip pacman=unzip zypper=unzip apk=unzip brew=unzip
fi
say "Installing Bun with the official installer"
pipe_sh https://bun.sh/install bash
ok "Bun is installed in ~/.bun (open a new shell to pick up PATH)"
