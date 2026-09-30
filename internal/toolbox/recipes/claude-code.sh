#!/usr/bin/env bash
# id: claude-code
# name: Claude Code
# category: agents
# description: Anthropic's agentic coding CLI (native installer, auto-updates).
# homepage: https://docs.anthropic.com/en/docs/claude-code
# check: claude
# version: claude --version
# requires-sudo: false
# platforms: linux, darwin
# size: 70 MB
# tags: agent, anthropic
say "Installing Claude Code with the official native installer"
pipe_sh https://claude.ai/install.sh bash
ok "Run 'claude' in a terminal to sign in"
