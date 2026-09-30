#!/usr/bin/env bash
# id: fd
# name: fd
# category: cli
# description: Simple, fast and user-friendly alternative to find.
# homepage: https://github.com/sharkdp/fd
# check: fd | fdfind
# version: {bin} --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 2 MB
# tags: find
say "Installing fd"
pkg apt=fd-find dnf=fd-find pacman=fd zypper=fd apk=fd brew=fd
if [ "$(pm)" = apt ] && ! have fd; then
  # Debian/Ubuntu ship the binary as fdfind; expose the usual name.
  link_bin "$(command -v fdfind || echo /usr/bin/fdfind)" fd
fi
ok "fd installed"
