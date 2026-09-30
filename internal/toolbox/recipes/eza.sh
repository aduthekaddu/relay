#!/usr/bin/env bash
# id: eza
# name: eza
# category: cli
# description: Modern, colourful replacement for ls.
# homepage: https://eza.rocks
# check: eza
# version: eza --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 2 MB
# tags: ls
say "Installing eza"
pkg apt="eza" dnf="eza" pacman="eza" zypper="eza" apk="eza" brew="eza"
ok "eza installed"
