#!/usr/bin/env bash
# id: desktop
# name: Remote desktop packages
# category: desktop
# description: TigerVNC, Openbox, tint2, xclip and xdotool — everything Relay's browser desktop needs.
# homepage: https://tigervnc.org
# check: Xvnc, openbox, tint2, xclip, xdotool
# version: Xvnc -version
# requires-sudo: true
# platforms: linux
# size: 60 MB
# tags: vnc, x11
say "Installing the remote desktop packages"
pkg \
  apt="tigervnc-standalone-server tigervnc-tools openbox tint2 xclip xdotool xterm dbus-x11 fonts-dejavu-core x11-xserver-utils" \
  dnf="tigervnc-server openbox tint2 xclip xdotool xterm dbus-x11 dejavu-sans-fonts xrandr" \
  pacman="tigervnc openbox tint2 xclip xdotool xterm ttf-dejavu xorg-xrandr" \
  zypper="xorg-x11-Xvnc openbox tint2 xclip xdotool xterm dejavu-fonts xrandr" \
  apk="tigervnc openbox tint2 xclip xdotool xterm font-dejavu xrandr"
ok "Desktop packages installed — open Desktop in Relay to start it"
