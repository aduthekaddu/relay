#!/usr/bin/env bash
# id: jq
# name: jq
# category: cli
# description: Slice, filter and transform JSON.
# homepage: https://jqlang.org
# check: jq
# version: jq --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 1 MB
# tags: json
say "Installing jq"
pkg apt="jq" dnf="jq" pacman="jq" zypper="jq" apk="jq" brew="jq"
ok "jq installed"
