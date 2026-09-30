#!/usr/bin/env bash
# id: copilot-cli
# name: GitHub Copilot CLI
# category: agents
# description: GitHub Copilot's coding agent in the terminal.
# homepage: https://github.com/github/copilot-cli
# check: copilot
# version: copilot --version
# requires-sudo: false
# platforms: linux, darwin
# size: 60 MB
# needs: node
# tags: agent, github
say "Installing GitHub Copilot CLI from npm"
npm_global @github/copilot@latest
ok "Run 'copilot' in a terminal, then '/login'"
