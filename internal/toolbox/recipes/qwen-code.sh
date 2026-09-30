#!/usr/bin/env bash
# id: qwen-code
# name: Qwen Code
# category: agents
# description: Alibaba's Qwen coding agent for the terminal.
# homepage: https://github.com/QwenLM/qwen-code
# check: qwen
# version: qwen --version
# requires-sudo: false
# platforms: linux, darwin
# size: 90 MB
# needs: node
# tags: agent
say "Installing Qwen Code from npm"
npm_global @qwen-code/qwen-code@latest
ok "Run 'qwen' in a terminal to sign in"
