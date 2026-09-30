#!/usr/bin/env bash
# id: fzf
# name: fzf
# category: cli
# description: Command-line fuzzy finder.
# homepage: https://github.com/junegunn/fzf
# check: fzf
# version: fzf --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 2 MB
# tags: fuzzy
say "Installing fzf"
pkg apt="fzf" dnf="fzf" pacman="fzf" zypper="fzf" apk="fzf" brew="fzf"
ok "fzf installed"
