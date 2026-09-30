#!/usr/bin/env bash
# id: bat
# name: bat
# category: cli
# description: cat with syntax highlighting and git integration.
# homepage: https://github.com/sharkdp/bat
# check: bat | batcat
# version: {bin} --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 5 MB
# tags: cat
say "Installing bat"
pkg apt=bat dnf=bat pacman=bat zypper=bat apk=bat brew=bat
if [ "$(pm)" = apt ] && ! have bat; then
  # Debian/Ubuntu ship the binary as batcat; expose the usual name.
  link_bin "$(command -v batcat || echo /usr/bin/batcat)" bat
fi
ok "bat installed"
