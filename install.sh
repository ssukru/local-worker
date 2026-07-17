#!/bin/sh
# Install the lw binary from the latest GitHub release:
#   curl -fsSL https://raw.githubusercontent.com/ssukru/local-worker/main/install.sh | sh
#
# Options (env vars):
#   LW_VERSION=v0.2.0        install a specific tag instead of the latest
#   LW_INSTALL_DIR=~/bin     install somewhere other than the default
#
# While the repo is private, unauthenticated downloads 404 — the script then
# falls back to the `gh` CLI, which must be installed and authenticated.
set -eu

REPO="ssukru/local-worker"

say() { printf '\033[1;36m[local-worker]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[local-worker]\033[0m %s\n' "$*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) die "Unsupported OS: $os. On Windows, run this inside WSL2 (wsl --install -d Ubuntu)." ;;
esac
arch=$(uname -m)
case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *) die "Unsupported architecture: $arch" ;;
esac
command -v curl >/dev/null || die "curl is required."

# Resolve the tag to install.
tag="${LW_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
    grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
fi
if [ -z "$tag" ] && command -v gh >/dev/null; then
  tag=$(gh release view -R "$REPO" --json tagName -q .tagName 2>/dev/null || true)
fi
[ -n "$tag" ] || die "Could not determine the latest release (private repo? install and authenticate gh, then re-run)."

asset="lw-$tag-$os-$arch.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading lw $tag ($os/$arch)…"
if ! curl -fsSL -o "$tmp/$asset" "https://github.com/$REPO/releases/download/$tag/$asset" 2>/dev/null; then
  command -v gh >/dev/null || die "Download failed and gh is not installed (private repo needs an authenticated gh)."
  gh release download "$tag" -R "$REPO" -p "$asset" -D "$tmp" || die "Download failed."
fi
tar -xzf "$tmp/$asset" -C "$tmp"

# Pick an install dir: explicit > /usr/local/bin if writable > ~/.local/bin.
bin_dir="${LW_INSTALL_DIR:-}"
if [ -z "$bin_dir" ]; then
  if [ -w /usr/local/bin ]; then
    bin_dir=/usr/local/bin
  else
    bin_dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$bin_dir"
install -m 0755 "$tmp/lw" "$bin_dir/lw"
say "Installed $("$bin_dir/lw" version) to $bin_dir/lw"

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) say "NOTE: $bin_dir is not on your PATH — add it to your shell profile." ;;
esac
command -v gh >/dev/null || say "NOTE: the GitHub CLI (gh) is required at runtime — lw setup can install it."
say "Next: run \`lw setup\` to attach this machine to your repos."
