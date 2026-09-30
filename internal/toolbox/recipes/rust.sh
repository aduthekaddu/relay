#!/usr/bin/env bash
# id: rust
# name: Rust (rustup)
# category: runtimes
# description: rustup, the official Rust toolchain installer, with the stable toolchain.
# homepage: https://rustup.rs
# check: rustc, cargo
# version: rustc --version
# requires-sudo: false
# platforms: linux, darwin
# size: 300 MB
# tags: cargo
say "Installing Rust with rustup"
pipe_sh https://sh.rustup.rs sh -y --profile default
ok "Rust is installed in ~/.cargo (open a new shell to pick up PATH)"
