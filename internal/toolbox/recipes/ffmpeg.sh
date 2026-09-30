#!/usr/bin/env bash
# id: ffmpeg
# name: FFmpeg
# category: creative
# description: Record, convert and stream audio and video.
# homepage: https://ffmpeg.org
# check: ffmpeg
# version: ffmpeg -version
# requires-sudo: linux
# platforms: linux, darwin
# size: 80 MB
# tags: video, audio
say "Installing FFmpeg"
pkg apt=ffmpeg dnf=ffmpeg-free pacman=ffmpeg zypper=ffmpeg apk=ffmpeg brew=ffmpeg
ok "FFmpeg installed"
