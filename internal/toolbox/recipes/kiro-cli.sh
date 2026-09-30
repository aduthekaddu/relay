#!/usr/bin/env bash
# id: kiro-cli
# name: Kiro CLI
# category: agents
# description: AWS Kiro agents in the terminal, with MCP, steering and custom agents.
# homepage: https://kiro.dev/cli/
# check: kiro-cli
# version: kiro-cli --version
# requires-sudo: false
# platforms: linux, darwin
# size: 80 MB
# tags: agent, aws
if ! have unzip; then
  say "Installing unzip (needed by the Kiro installer)"
  pkg apt=unzip dnf=unzip pacman=unzip zypper=unzip apk=unzip brew=unzip
fi
say "Installing Kiro CLI with the official installer"
pipe_sh https://cli.kiro.dev/install bash
ok "Run 'kiro-cli login' in a terminal to sign in"
