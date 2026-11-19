#!/bin/sh
# Install or upgrade lw from a verified GitHub release:
#   curl -fsSL https://raw.githubusercontent.com/ssukru/local-worker/main/install.sh | sh
#
# Options (env vars):
#   LW_VERSION=0.3.0          install a specific release (leading v is optional)
#   LW_INSTALL_DIR="$HOME/bin" install in an explicit directory
set -eu

REPO="ssukru/local-worker"

say() { printf '\033[1;36m[local-worker]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[local-worker]\033[0m %s\n' "$*" >&2; exit 1; }

resolve_path() {
  value=$1
  hops=0
  while [ -L "$value" ]; do
    hops=$((hops + 1))
    [ "$hops" -le 40 ] || die "Too many symlinks while resolving $1"
    dir=$(CDPATH= cd -P "$(dirname "$value")" && pwd)
    link=$(readlink "$value")
    case "$link" in
      /*) value=$link ;;
      *) value=$dir/$link ;;
    esac
  done
  dir=$(CDPATH= cd -P "$(dirname "$value")" && pwd)
  printf '%s/%s\n' "$dir" "$(basename "$value")"
}

absolute_path() {
  value=$1
  case "$value" in
    /*) printf '%s\n' "$value" ;;
    *) printf '%s/%s\n' "$(pwd)" "$value" ;;
  esac
}

is_homebrew_path() {
  case "$1" in
    */Cellar/local-worker/*) return 0 ;;
    *) return 1 ;;
  esac
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) die "Unsupported OS: $os. On Windows, run this inside WSL2 (wsl --install -d Ubuntu)." ;;
esac
arch=$(uname -m)
case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) die "Unsupported architecture: $arch" ;;
esac
command -v curl >/dev/null || die "curl is required."
command -v tar >/dev/null || die "tar is required."
[ "$(id -u)" -ne 0 ] || die "Do not run this installer as root or with sudo. Use a normal user account."

system_bin_dir=${LW_SYSTEM_BIN_DIR:-/usr/local/bin}

# Find every distinct existing installation before selecting a destination.
tmp=$(mktemp -d)
staged=""
trap '[ -z "$staged" ] || rm -f "$staged"; rm -rf "$tmp"' EXIT HUP INT TERM
candidates="$tmp/candidates"
resolved_candidates="$tmp/resolved-candidates"
: >"$candidates"
: >"$resolved_candidates"

add_candidate() {
  candidate=$1
  case "$candidate" in *'|'*) die "Unsupported character in installation path: $candidate" ;; esac
  [ -e "$candidate" ] || [ -L "$candidate" ] || return 0
  [ -x "$candidate" ] || die "Existing path is not an executable lw binary: $candidate"
  resolved=$(resolve_path "$candidate") || die "Could not resolve existing lw at $candidate"
  [ -f "$resolved" ] || die "Existing lw target is not a regular file: $resolved"
  if ! grep -F -x "$resolved" "$resolved_candidates" >/dev/null 2>&1; then
    printf '%s\n' "$resolved" >>"$resolved_candidates"
    printf '%s|%s\n' "$candidate" "$resolved" >>"$candidates"
  fi
}

old_ifs=$IFS
IFS=:
for dir in $PATH; do
  [ -n "$dir" ] || dir=.
  add_candidate "$dir/lw"
done
IFS=$old_ifs
add_candidate "$HOME/.local/bin/lw"
add_candidate "$system_bin_dir/lw"
explicit_target=""
if [ -n "${LW_INSTALL_DIR:-}" ]; then
  explicit_target=$(absolute_path "$LW_INSTALL_DIR/lw")
  add_candidate "$LW_INSTALL_DIR/lw"
fi

while IFS='|' read -r invocation resolved; do
  [ -n "$invocation" ] || continue
  invocation=$(absolute_path "$invocation")
  case "$invocation" in
    "$HOME/.local/bin/lw"|"$system_bin_dir/lw"|"$explicit_target") ;;
    *)
      if is_homebrew_path "$resolved"; then
        die "lw is managed by Homebrew. Run: brew update && brew upgrade local-worker"
      fi
      die "Found lw at $invocation. Re-run with LW_INSTALL_DIR=$(dirname "$invocation") to manage that explicit installation."
      ;;
  esac
done <"$candidates"

candidate_count=$(wc -l <"$candidates" | tr -d ' ')
if [ "$candidate_count" -gt 1 ]; then
  say "Multiple lw installations were found:"
  while IFS='|' read -r invocation resolved; do
    printf '  %s -> %s\n' "$invocation" "$resolved" >&2
  done <"$candidates"
  die "Remove the duplicate installation or set PATH to a single lw before retrying."
fi

if [ "$candidate_count" -eq 1 ]; then
  candidate=$(cut -d'|' -f1 "$candidates")
  target=$(cut -d'|' -f2- "$candidates")
  if is_homebrew_path "$target"; then
    die "lw is managed by Homebrew. Run: brew update && brew upgrade local-worker"
  fi
  if [ -n "${LW_INSTALL_DIR:-}" ]; then
    requested=$explicit_target
    if [ -e "$explicit_target" ] || [ -L "$explicit_target" ]; then
      requested=$(resolve_path "$explicit_target")
    fi
    if [ "$(absolute_path "$candidate")" != "$explicit_target" ] && [ "$target" != "$requested" ]; then
      die "lw already exists at $candidate; refusing to create a second installation in $LW_INSTALL_DIR."
    fi
  fi
else
  if [ -n "${LW_INSTALL_DIR:-}" ]; then
    target=$explicit_target
  else
    target="$HOME/.local/bin/lw"
  fi
fi

bin_dir=$(dirname "$target")
mkdir -p "$bin_dir" || die "Could not create $bin_dir"
[ -w "$bin_dir" ] || die "$bin_dir is not writable. Choose LW_INSTALL_DIR or fix its permissions."

# Resolve the release tag.
tag=${LW_VERSION:-}
if [ -n "$tag" ]; then
  case "$tag" in v*) ;; *) tag=v$tag ;; esac
  printf '%s\n' "$tag" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$' >/dev/null ||
    die "LW_VERSION must be a semantic version such as 0.3.0 or v0.3.0."
else
  tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
    grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
fi
if [ -z "$tag" ] && command -v gh >/dev/null; then
  tag=$(gh release view -R "$REPO" --json tagName -q .tagName 2>/dev/null || true)
fi
[ -n "$tag" ] || die "Could not determine the latest release. Check network access or authenticate gh and retry."

asset="lw-$tag-$os-$arch.tar.gz"

download_asset() {
  name=$1
  url="https://github.com/$REPO/releases/download/$tag/$name"
  rm -f "$tmp/$name"
  if curl -fsSL -o "$tmp/$name" "$url" 2>/dev/null; then
    return 0
  fi
  rm -f "$tmp/$name"
  command -v gh >/dev/null || die "Download failed. Install/authenticate gh or check access to $REPO."
  gh release download "$tag" -R "$REPO" -p "$name" -O "$tmp/$name" || die "Download failed for $name."
}

say "Downloading lw $tag ($os/$arch)…"
download_asset "$asset"
download_asset "checksums.txt"

expected=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$tmp/checksums.txt")
match_count=$(printf '%s\n' "$expected" | awk 'NF { count++ } END { print count+0 }')
[ "$match_count" -eq 1 ] || die "checksums.txt must contain exactly one entry for $asset."
expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
printf '%s\n' "$expected" | grep -E '^[0-9a-f]{64}$' >/dev/null || die "Invalid SHA-256 entry for $asset."
if command -v shasum >/dev/null; then
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
elif command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  die "shasum or sha256sum is required to verify the download."
fi
[ "$actual" = "$expected" ] || die "Checksum verification failed for $asset; the existing lw was not changed."

entries=$(tar -tzf "$tmp/$asset") || die "Downloaded archive is invalid."
entry_count=$(printf '%s\n' "$entries" | awk 'NF { count++ } END { print count+0 }')
[ "$entry_count" -eq 1 ] || die "Release archive must contain exactly one file."
case "$entries" in lw|./lw) ;; *) die "Release archive contains an unexpected path: $entries" ;; esac
mkdir "$tmp/extract"
tar -xzf "$tmp/$asset" -C "$tmp/extract" || die "Could not extract the release archive."
payload="$tmp/extract/lw"
[ -f "$payload" ] && [ ! -L "$payload" ] || die "Release archive does not contain a regular lw binary."

staged=$(mktemp "$bin_dir/.lw-install.XXXXXX") || die "Could not stage the update in $bin_dir."
install -m 0755 "$payload" "$staged" || die "Could not stage lw in $bin_dir."
installed_version=$("$staged" version 2>/dev/null || true)
expected_version="lw ${tag#v}"
[ "$installed_version" = "$expected_version" ] ||
  die "Downloaded binary reports '$installed_version', expected '$expected_version'; the existing lw was not changed."

existing=false
if [ -e "$target" ] || [ -L "$target" ]; then
  existing=true
fi
mv -f "$staged" "$target" || die "Could not replace $target."
staged=""
if [ "$existing" = true ]; then
  say "Replaced the existing installation with $installed_version at $target"
else
  say "Installed $installed_version to $target"
fi

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *)
    say "NOTE: $bin_dir is not on PATH. Add this to your shell profile:"
    printf '  export PATH="%s:$PATH"\n' "$bin_dir"
    ;;
esac
command -v gh >/dev/null || say "NOTE: install the GitHub CLI (gh) before running lw setup."
say "Next: run \`lw setup\` to attach this machine to your repos."
