#!/usr/bin/env bash
# id: tmux
# name: tmux
# category: cli
# description: Terminal multiplexer (Relay can attach existing tmux sessions).
# homepage: https://github.com/tmux/tmux
# check: tmux
# version: tmux -V
# requires-sudo: linux
# platforms: linux, darwin
# size: 1 MB
# tags: terminal
say "Installing tmux"
pkg apt="tmux" dnf="tmux" pacman="tmux" zypper="tmux" apk="tmux" brew="tmux"
ok "tmux installed"
