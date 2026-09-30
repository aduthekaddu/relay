#!/usr/bin/env bash
# id: gemini-cli
# name: Gemini CLI
# category: agents
# description: Google's open-source AI agent for the terminal.
# homepage: https://github.com/google-gemini/gemini-cli
# check: gemini
# version: gemini --version
# requires-sudo: false
# platforms: linux, darwin
# size: 90 MB
# needs: node
# tags: agent, google
say "Installing Gemini CLI from npm"
npm_global @google/gemini-cli@latest
ok "Run 'gemini' in a terminal to sign in"
