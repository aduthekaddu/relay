#!/usr/bin/env bash
# id: blender
# name: Blender
# category: creative
# description: 3D creation suite; pairs with the Blender MCP server for agent-driven scenes.
# homepage: https://www.blender.org
# check: blender | /Applications/Blender.app/Contents/MacOS/Blender
# version: {bin} --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 350 MB
# tags: 3d
say "Installing Blender"
pkg apt=blender dnf=blender pacman=blender zypper=blender apk=blender brew="--cask blender"
ok "Blender installed — use it from the remote desktop or headless with 'blender -b'"
