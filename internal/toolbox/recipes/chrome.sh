#!/usr/bin/env bash
# id: chrome
# name: Google Chrome / Chromium
# category: browsers
# description: A real browser for the remote desktop and browser-driving MCP servers (Chromium on ARM Linux).
# homepage: https://www.google.com/chrome/
# check: google-chrome | google-chrome-stable | chromium | chromium-browser | /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
# version: {bin} --version
# requires-sudo: linux
# platforms: linux, darwin
# size: 110 MB
# tags: browser, playwright
mgr="$(pm)"
if [ "$mgr" = brew ]; then
  pkg brew="--cask google-chrome"
elif [ "$RELAY_ARCH" = amd64 ] && [ "$mgr" = apt ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "Downloading Google Chrome (stable)"
  download https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb "$tmp/chrome.deb"
  run chmod 0644 "$tmp/chrome.deb"
  run chmod 0755 "$tmp"
  as_root apt-get update -q
  as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -q "$tmp/chrome.deb"
elif [ "$RELAY_ARCH" = amd64 ] && [ "$mgr" = dnf ]; then
  as_root dnf install -y https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm
else
  say "Google Chrome is not built for $RELAY_OS/$RELAY_ARCH — installing Chromium"
  if [ "$(os_id)" = ubuntu ]; then
    need snap "Ubuntu ships Chromium as a snap"
    as_root snap install chromium
  else
    pkg apt=chromium dnf=chromium pacman=chromium zypper=chromium apk=chromium
  fi
fi
ok "Browser installed"
