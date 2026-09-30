#!/usr/bin/env bash
# id: codex
# name: Codex CLI
# category: agents
# description: OpenAI's coding agent for the terminal.
# homepage: https://github.com/openai/codex
# check: codex
# version: codex --version
# requires-sudo: false
# platforms: linux, darwin
# size: 60 MB
# needs: node
# tags: agent, openai
say "Installing Codex CLI from npm"
npm_global @openai/codex@latest
ok "Run 'codex' in a terminal to sign in"
