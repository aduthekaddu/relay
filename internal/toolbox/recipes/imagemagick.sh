#!/usr/bin/env bash
# id: imagemagick
# name: ImageMagick
# category: creative
# description: Convert, resize and edit images from the command line.
# homepage: https://imagemagick.org
# check: magick | convert
# version: {bin} -version
# requires-sudo: linux
# platforms: linux, darwin
# size: 30 MB
# tags: images
say "Installing ImageMagick"
pkg apt=imagemagick dnf=ImageMagick pacman=imagemagick zypper=ImageMagick apk=imagemagick brew=imagemagick
ok "ImageMagick installed"
