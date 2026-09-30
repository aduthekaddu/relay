#!/usr/bin/env bash
# id: python
# name: Python (uv)
# category: runtimes
# description: Astral's uv — fast Python package and project manager — plus a managed CPython.
# homepage: https://docs.astral.sh/uv/
# check: uv
# version: uv --version
# requires-sudo: false
# platforms: linux, darwin
# size: 60 MB
# tags: python, uv, uvx
say "Installing uv with the official installer"
pipe_sh https://astral.sh/uv/install.sh sh
uv_bin="$HOME/.local/bin/uv"
have uv && uv_bin="$(command -v uv)"
say "Installing a managed Python"
run "$uv_bin" python install
ok "uv, uvx and Python are ready"
