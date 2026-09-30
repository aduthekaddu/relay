#!/usr/bin/env bash
# id: cursor-agent
# name: Cursor CLI
# category: agents
# description: Cursor's agent in the terminal (cursor-agent).
# homepage: https://cursor.com/docs/cli/overview
# check: cursor-agent
# version: cursor-agent --version
# requires-sudo: false
# platforms: linux, darwin
# size: 60 MB
# tags: agent, cursor
say "Installing Cursor CLI with the official installer"
pipe_sh https://cursor.com/install bash
ok "Run 'cursor-agent login' in a terminal to sign in"
