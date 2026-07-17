#!/bin/bash
# Bootstrap local-worker on a fresh Mac:
#   git clone <this repo> && cd local-worker && ./install.sh
# Installs Homebrew/Node/gh if missing, then launches the setup wizard.
set -euo pipefail

say() { printf '\033[1;36m[local-worker]\033[0m %s\n' "$*"; }

if [[ "$(uname)" != "Darwin" ]]; then
  echo "local-worker targets macOS." >&2
  exit 1
fi

if ! command -v brew >/dev/null; then
  say "Installing Homebrew…"
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  eval "$([[ -d /opt/homebrew ]] && /opt/homebrew/bin/brew shellenv || /usr/local/bin/brew shellenv)"
fi

command -v git >/dev/null || xcode-select --install
command -v node >/dev/null || { say "Installing Node…"; brew install node; }
command -v gh >/dev/null || { say "Installing GitHub CLI…"; brew install gh; }

cd "$(dirname "$0")"
say "Installing CLI dependencies…"
if command -v pnpm >/dev/null; then pnpm install --silent; else npm install --silent; fi

say "Linking the lw command…"
npm link --silent 2>/dev/null || sudo npm link --silent

say "Done. Starting the setup wizard…"
exec lw setup
