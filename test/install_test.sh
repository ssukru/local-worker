#!/bin/sh
set -eu

SCRIPT=$(CDPATH= cd "$(dirname "$0")/.." && pwd)/install.sh
SYSTEM_PATH=/usr/bin:/bin:/usr/sbin:/sbin
passed=0

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

make_fixture() {
  root=$1
  mkdir -p "$root/fakebin" "$root/fixture" "$root/system"

  cat >"$root/fixture/lw" <<'BINARY'
#!/bin/sh
if [ "${1:-}" = version ]; then
  echo "lw 0.3.0"
  exit 0
fi
exit 1
BINARY
  chmod 755 "$root/fixture/lw"
  tar -C "$root/fixture" -czf "$root/fixture/archive" lw
  if command -v shasum >/dev/null 2>&1; then
    digest=$(shasum -a 256 "$root/fixture/archive" | awk '{print $1}')
  else
    digest=$(sha256sum "$root/fixture/archive" | awk '{print $1}')
  fi
  printf '%s  lw-v0.3.0-darwin-arm64.tar.gz\n' "$digest" >"$root/fixture/checksums"

  cat >"$root/fakebin/uname" <<'FAKE_UNAME'
#!/bin/sh
case "${1:-}" in
  -s) echo Darwin ;;
  -m) echo arm64 ;;
  *) echo Darwin ;;
esac
FAKE_UNAME
  chmod 755 "$root/fakebin/uname"

  cat >"$root/fakebin/curl" <<'FAKE_CURL'
#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    http*) url=$1; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  *api.github.com*) printf '%s\n' '{"tag_name":"v0.3.0"}' ;;
  *releases/download*checksums.txt)
    [ "${CURL_FAIL_DOWNLOAD:-0}" = 1 ] && exit 1
    cp "$FIXTURE_DIR/checksums" "$out"
    ;;
  *releases/download*)
    [ "${CURL_FAIL_DOWNLOAD:-0}" = 1 ] && exit 1
    cp "$FIXTURE_DIR/archive" "$out"
    ;;
  *) exit 1 ;;
esac
FAKE_CURL
  chmod 755 "$root/fakebin/curl"

  cat >"$root/fakebin/gh" <<'FAKE_GH'
#!/bin/sh
case "${1:-} ${2:-}" in
  "release view") echo v0.3.0 ;;
  "release download")
    name=""
    out=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        -p) name=$2; shift 2 ;;
        -O) out=$2; shift 2 ;;
        *) shift ;;
      esac
    done
    case "$name" in
      checksums.txt) cp "$FIXTURE_DIR/checksums" "$out" ;;
      *) cp "$FIXTURE_DIR/archive" "$out" ;;
    esac
    ;;
  *) exit 1 ;;
esac
FAKE_GH
  chmod 755 "$root/fakebin/gh"
}

run_installer() {
  root=$1
  shift
  env HOME="$root/home" FIXTURE_DIR="$root/fixture" LW_SYSTEM_BIN_DIR="$root/system" PATH="$root/fakebin:$SYSTEM_PATH" "$@" sh "$SCRIPT"
}

new_case() {
  root=$(mktemp -d)
  mkdir -p "$root/home"
  make_fixture "$root"
  printf '%s\n' "$root"
}

case_fresh_install() {
  root=$(new_case)
  output=$(run_installer "$root" 2>&1) || fail "fresh install failed: $output"
  [ -x "$root/home/.local/bin/lw" ] || fail "fresh install did not use ~/.local/bin"
  [ "$("$root/home/.local/bin/lw" version)" = "lw 0.3.0" ] || fail "fresh binary version mismatch"
  printf '%s' "$output" | grep -F 'not on PATH' >/dev/null || fail "fresh install omitted PATH guidance"
  rm -rf "$root"
}

case_existing_install_upgrades_in_place() {
  root=$(new_case)
  mkdir -p "$root/legacy"
  cat >"$root/legacy/lw" <<'OLD'
#!/bin/sh
echo "lw 0.2.0"
OLD
  chmod 755 "$root/legacy/lw"
  output=$(env HOME="$root/home" FIXTURE_DIR="$root/fixture" LW_SYSTEM_BIN_DIR="$root/legacy" PATH="$root/fakebin:$root/legacy:$SYSTEM_PATH" sh "$SCRIPT" 2>&1) ||
    fail "existing install upgrade failed: $output"
  [ "$("$root/legacy/lw" version)" = "lw 0.3.0" ] || fail "existing install was not upgraded"
  [ ! -e "$root/home/.local/bin/lw" ] || fail "upgrade created a duplicate user-local binary"
  rm -rf "$root"
}

case_checksum_failure_preserves_binary() {
  root=$(new_case)
  mkdir -p "$root/direct"
  cat >"$root/direct/lw" <<'OLD'
#!/bin/sh
echo "lw 0.2.0"
OLD
  chmod 755 "$root/direct/lw"
  printf '%064d  lw-v0.3.0-darwin-arm64.tar.gz\n' 0 >"$root/fixture/checksums"
  if env HOME="$root/home" FIXTURE_DIR="$root/fixture" LW_SYSTEM_BIN_DIR="$root/direct" PATH="$root/fakebin:$root/direct:$SYSTEM_PATH" sh "$SCRIPT" >"$root/out" 2>&1; then
    fail "checksum mismatch unexpectedly succeeded"
  fi
  [ "$("$root/direct/lw" version)" = "lw 0.2.0" ] || fail "checksum failure changed old binary"
  grep -F 'Checksum verification failed' "$root/out" >/dev/null || fail "checksum error was not actionable"
  rm -rf "$root"
}

case_duplicate_install_refused() {
  root=$(new_case)
  mkdir -p "$root/home/.local/bin" "$root/system"
  printf '#!/bin/sh\necho lw 0.2.0\n' >"$root/home/.local/bin/lw"
  printf '#!/bin/sh\necho lw 0.1.0\n' >"$root/system/lw"
  chmod 755 "$root/home/.local/bin/lw" "$root/system/lw"
  if env HOME="$root/home" FIXTURE_DIR="$root/fixture" LW_SYSTEM_BIN_DIR="$root/system" PATH="$root/fakebin:$root/home/.local/bin:$root/system:$SYSTEM_PATH" sh "$SCRIPT" >"$root/out" 2>&1; then
    fail "duplicate install unexpectedly succeeded"
  fi
  grep -F 'Multiple lw installations' "$root/out" >/dev/null || fail "duplicate error was not actionable"
  rm -rf "$root"
}

case_homebrew_install_refused() {
  root=$(new_case)
  cellar="$root/Cellar/local-worker/0.2.0/bin"
  mkdir -p "$cellar" "$root/brew-bin"
  printf '#!/bin/sh\necho lw 0.2.0\n' >"$cellar/lw"
  chmod 755 "$cellar/lw"
  ln -s "$cellar/lw" "$root/brew-bin/lw"
  if env HOME="$root/home" FIXTURE_DIR="$root/fixture" LW_SYSTEM_BIN_DIR="$root/system" PATH="$root/fakebin:$root/brew-bin:$SYSTEM_PATH" sh "$SCRIPT" >"$root/out" 2>&1; then
    fail "Homebrew install unexpectedly succeeded"
  fi
  grep -F 'brew update && brew upgrade local-worker' "$root/out" >/dev/null || fail "Homebrew error omitted upgrade command"
  rm -rf "$root"
}

case_gh_download_fallback() {
  root=$(new_case)
  output=$(run_installer "$root" CURL_FAIL_DOWNLOAD=1 2>&1) || fail "gh fallback failed: $output"
  [ "$("$root/home/.local/bin/lw" version)" = "lw 0.3.0" ] || fail "gh fallback binary mismatch"
  rm -rf "$root"
}

for test_case in \
  case_fresh_install \
  case_existing_install_upgrades_in_place \
  case_checksum_failure_preserves_binary \
  case_duplicate_install_refused \
  case_homebrew_install_refused \
  case_gh_download_fallback
do
  "$test_case"
  passed=$((passed + 1))
  printf 'ok %d - %s\n' "$passed" "$test_case"
done

printf '%d installer tests passed\n' "$passed"
