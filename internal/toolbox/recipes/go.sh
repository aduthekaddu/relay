#!/usr/bin/env bash
# id: go
# name: Go
# category: runtimes
# description: The latest Go toolchain from go.dev, installed under your home directory.
# homepage: https://go.dev
# check: go
# version: go version
# requires-sudo: false
# platforms: linux, darwin
# size: 70 MB
# tags: golang
say "Looking up the latest Go release"
v="$(fetch 'https://go.dev/VERSION?m=text' go1.0.0 | head -n1)"
case "$v" in go1.*) ;; *) die "unexpected Go version string: $v" ;; esac
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
file="$v.$RELAY_OS-$RELAY_ARCH.tar.gz"
say "Downloading $file"
download "https://go.dev/dl/$file" "$tmp/$file"
run mkdir -p "$TOOLS_DIR"
run rm -rf "${TOOLS_DIR:?}/go"
run tar -xzf "$tmp/$file" -C "$TOOLS_DIR"
link_bin "$TOOLS_DIR/go/bin/go"
link_bin "$TOOLS_DIR/go/bin/gofmt"
ok "$v installed to $TOOLS_DIR/go (add ~/go/bin to PATH for 'go install' tools)"
