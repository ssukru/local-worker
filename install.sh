#!/bin/bash
# Bootstrap local-worker on a fresh machine (macOS or Linux; Windows via WSL2):
#   git clone <this repo> && cd local-worker && ./install.sh
# Installs Node/gh if missing, then launches the setup wizard.
set -euo pipefail

say() { printf '\033[1;36m[local-worker]\033[0m %s\n' "$*"; }

OS="$(uname)"

if [[ "$OS" == "Darwin" ]]; then
  if ! command -v brew >/dev/null; then
    say "Installing Homebrew…"
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    eval "$([[ -d /opt/homebrew ]] && /opt/homebrew/bin/brew shellenv || /usr/local/bin/brew shellenv)"
  fi
  command -v git >/dev/null || xcode-select --install
  command -v node >/dev/null || { say "Installing Node…"; brew install node; }
  command -v gh >/dev/null || { say "Installing GitHub CLI…"; brew install gh; }
elif [[ "$OS" == "Linux" ]]; then
  if ! command -v systemctl >/dev/null; then
    echo "local-worker needs systemd on Linux (on WSL2: enable systemd in /etc/wsl.conf)." >&2
    exit 1
  fi
  install_pkg() {
    if command -v apt-get >/dev/null; then sudo apt-get update -qq && sudo apt-get install -y "$@"
    elif command -v dnf >/dev/null; then sudo dnf install -y "$@"
    elif command -v pacman >/dev/null; then sudo pacman -S --noconfirm "$@"
    else return 1; fi
  }
  command -v git >/dev/null || { say "Installing git…"; install_pkg git; }
  command -v curl >/dev/null || { say "Installing curl…"; install_pkg curl; }
  if ! command -v node >/dev/null; then
    say "Installing Node…"
    install_pkg nodejs npm || {
      echo "Could not auto-install Node — install Node 20+ (https://nodejs.org) and re-run." >&2
      exit 1
    }
  fi
  if ! command -v gh >/dev/null; then
    say "Installing GitHub CLI…"
    install_pkg gh || {
      echo "Could not auto-install gh — see https://github.com/cli/cli#installation and re-run." >&2
      exit 1
    }
  fi
else
  echo "Unsupported OS: $OS. On Windows, run this inside WSL2 (wsl --install -d Ubuntu)." >&2
  exit 1
fi

cd "$(dirname "$0")"
say "Installing CLI dependencies…"
if command -v pnpm >/dev/null; then pnpm install --silent; else npm install --silent; fi

say "Linking the lw command…"
npm link --silent 2>/dev/null || sudo npm link --silent

say "Done. Starting the setup wizard…"
exec lw setup
