#!/usr/bin/env bash
# id: opencode
# name: OpenCode
# category: agents
# description: Open-source coding agent with any model provider.
# homepage: https://opencode.ai
# check: opencode
# version: opencode --version
# requires-sudo: false
# platforms: linux, darwin
# size: 50 MB
# tags: agent
say "Installing OpenCode with the official installer"
pipe_sh https://opencode.ai/install bash
ok "Run 'opencode' in a terminal, then '/connect' to add a provider"
