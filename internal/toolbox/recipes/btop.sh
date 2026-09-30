#!/usr/bin/env bash
# id: btop
# name: btop
# category: cli
# description: Resource monitor with a friendly UI.
# homepage: https://github.com/aristocratos/btop
# check: btop
# version: btop --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 2 MB
# tags: monitor
say "Installing btop"
pkg apt="btop" dnf="btop" pacman="btop" zypper="btop" apk="btop" brew="btop"
ok "btop installed"
