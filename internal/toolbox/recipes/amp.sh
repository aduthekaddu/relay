#!/usr/bin/env bash
# id: amp
# name: Amp
# category: agents
# description: Sourcegraph's frontier coding agent CLI.
# homepage: https://ampcode.com
# check: amp
# version: amp --version
# requires-sudo: false
# platforms: linux, darwin
# size: 60 MB
# tags: agent
say "Installing Amp with the official installer"
pipe_sh https://ampcode.com/install.sh bash
ok "Run 'amp' in a terminal to sign in"
