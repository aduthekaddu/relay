#!/usr/bin/env bash
# id: ripgrep
# name: ripgrep
# category: cli
# description: Recursively search directories for a regex, fast (rg).
# homepage: https://github.com/BurntSushi/ripgrep
# check: rg
# version: rg --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 2 MB
# tags: search
say "Installing ripgrep"
pkg apt="ripgrep" dnf="ripgrep" pacman="ripgrep" zypper="ripgrep" apk="ripgrep" brew="ripgrep"
ok "ripgrep installed"
