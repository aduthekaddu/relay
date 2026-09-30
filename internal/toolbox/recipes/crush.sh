#!/usr/bin/env bash
# id: crush
# name: Crush
# category: agents
# description: Charm's glamorous terminal coding agent, any provider.
# homepage: https://github.com/charmbracelet/crush
# check: crush
# version: crush --version
# requires-sudo: false
# platforms: linux, darwin
# size: 40 MB
# tags: agent, charm
say "Installing Crush"
if [ "$(pm)" = brew ]; then
  pkg brew=charmbracelet/tap/crush
else
  npm_global @charmland/crush@latest
fi
ok "Run 'crush' in a repository"
