#!/usr/bin/env bash
# id: code-server
# name: code-server
# category: editors
# description: VS Code in the browser; Relay serves it at /apps/code behind your login.
# homepage: https://github.com/coder/code-server
# check: code-server
# version: code-server --version
# requires-sudo: false
# platforms: linux, darwin
# size: 250 MB
# tags: vscode, ide
say "Installing code-server (standalone, into ~/.local)"
pipe_sh https://code-server.dev/install.sh sh --method=standalone --prefix="$HOME/.local"
ok "code-server installed — open Code in Relay"
