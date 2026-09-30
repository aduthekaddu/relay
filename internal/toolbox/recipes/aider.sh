#!/usr/bin/env bash
# id: aider
# name: Aider
# category: agents
# description: AI pair programming in your terminal, git-native.
# homepage: https://aider.chat
# check: aider
# version: aider --version
# requires-sudo: false
# platforms: linux, darwin
# size: 150 MB
# tags: agent, python
say "Installing Aider with the official installer (uses uv, isolated Python)"
pipe_sh https://aider.chat/install.sh sh
ok "Set an API key (e.g. ANTHROPIC_API_KEY) and run 'aider' in a repository"
