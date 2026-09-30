#!/usr/bin/env bash
# id: gh
# name: GitHub CLI
# category: cli
# description: GitHub on the command line — PRs, issues, releases, auth for git.
# homepage: https://cli.github.com
# check: gh
# version: gh --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 40 MB
# tags: git, github
mgr="$(pm)"
if [ "$mgr" = apt ]; then
  say "Adding the official GitHub CLI apt repository"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  download https://cli.github.com/packages/githubcli-archive-keyring.gpg "$tmp/gh.gpg"
  as_root mkdir -p -m 755 /etc/apt/keyrings
  as_root install -m 0644 "$tmp/gh.gpg" /etc/apt/keyrings/githubcli-archive-keyring.gpg
  arch="$(dpkg --print-architecture 2>/dev/null || echo "$RELAY_ARCH")"
  echo "deb [arch=$arch signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" |
    sudo_write /etc/apt/sources.list.d/github-cli.list
  pkg apt=gh
else
  pkg dnf=gh pacman=github-cli zypper=gh apk=github-cli brew=gh
fi
ok "GitHub CLI installed — run 'gh auth login'"
