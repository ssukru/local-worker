# local-worker

Turn a spare Mac (or Linux box) into a free, always-on CI machine for your
GitHub repos.

Your workflows stay in GitHub Actions (same YAML, same secrets, same UI) — but
jobs execute on your own hardware. GitHub charges **nothing** for self-hosted
runner minutes, so builds and deploys cost you only electricity, and an
Apple Silicon Mac will usually outrun GitHub's hosted runners anyway.
Perfect for iOS builds too, where hosted macOS minutes bill at a 10× multiplier.

```
you (git push / manual dispatch)
        │
   GitHub Actions   ← queue, logs, secrets — free
        │  outbound long-poll (works behind home NAT, no port-forwarding)
        ▼
   your Mac (lw)    ← runs the job: build, test, deploy, xcodebuild…
```

## Platform support

| Platform | Status | Services via | Notes |
| --- | --- | --- | --- |
| macOS (Apple Silicon & Intel) | ✅ first-class | launchd LaunchAgents | only platform that can build iOS |
| Linux (systemd distros) | ✅ supported | systemd user units | native amd64 Docker builds |
| Windows | via WSL2 | systemd (inside WSL) | see below; native support: PRs welcome |

**Windows / WSL2:** `wsl --install -d Ubuntu`, enable systemd in `/etc/wsl.conf`
(`[boot]` → `systemd=true`), then install local-worker inside WSL (see
Install below). Your jobs run in Linux — the same environment as GitHub's
`ubuntu-*` hosted runners.

## Install (on the machine that will do the work)

`lw` is a single static Go binary. The quickest path on a new Mac or Linux
machine is the verified install script:

```bash
curl -fsSL https://raw.githubusercontent.com/ssukru/local-worker/main/install.sh | sh
```

A fresh script install always goes to `~/.local/bin/lw`, never uses `sudo`
(and refuses to run as root), and verifies the release checksum before
replacing anything. If that directory is
not already on PATH, add it to your shell profile:

```bash
# zsh (default on macOS)
printf '%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zprofile

# bash
printf '%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

Restart the shell, then install the [GitHub CLI (`gh`)](https://cli.github.com/)
if it is not present. When Homebrew is available, `lw setup` can offer to run
`brew install gh`; otherwise it prints the direct installation requirement.

**Homebrew-managed alternative** (macOS & Linux):

```bash
brew tap ssukru/local-worker https://github.com/ssukru/local-worker.git
brew trust ssukru/local-worker   # newer Homebrew requires trusting third-party taps
brew install local-worker
```

**From source** (needs the Go version declared in `go.mod`):

```bash
git clone https://github.com/ssukru/local-worker.git
cd local-worker
go build -ldflags "-X main.version=dev" -o "$HOME/.local/bin/lw" ./cmd/lw
```

Then run `lw setup`. The guided wizard signs you into GitHub (browser
device-flow — no tokens to copy around), checks the machine (power settings,
disk, Docker, Xcode), lets you pick which repos send jobs here, and installs
everything as launchd services that survive reboots.

## Commands

| Command | What it does |
| --- | --- |
| `lw setup` | Guided first-time setup |
| `lw add owner/repo` | Attach this machine as a runner for a repo |
| `lw remove owner/repo` | Deregister + clean up |
| `lw status` | Local *and* GitHub's view of every runner |
| `lw doctor` | Diagnose machine readiness (sleep, auto-login, tools…) |
| `lw watchdog install\|uninstall\|check\|status` | Self-healing checks + offline alerting |
| `lw vm status\|refresh owner/repo` | Inspect / rebuild isolated environments |
| `lw integrate owner/repo` | Copy-paste workflow patch to route jobs here |
| `lw logs [owner/repo]` | Tail a runner's log |
| `lw update` | Check for and install a verified release |

`lw add` flags: `--labels a,b,c` · `--name <runner-name>` ·
`--isolation host|vm|container` · `--vm-image <image>` ·
`--refresh never|per-job|<n>h` · `--yes` (non-interactive).

## Update

Direct/script installations update themselves in place:

```bash
lw update --check          # compare the installed and latest versions
lw update                  # confirm and install the latest stable release
lw update --to 0.3.0       # install an exact release
lw update --yes            # unattended update
```

`--force` is required to reinstall the same version, downgrade, or replace a
source build that reports `dev`. Every download is checked against the
release's SHA-256 manifest and staged beside the existing binary before an
atomic replacement.

Homebrew owns its Cellar files, so `lw update` will not overwrite them. Use:

```bash
brew update && brew upgrade local-worker
```

The install script upgrades recognized direct installations in place:
`~/.local/bin/lw`, an older `/usr/local/bin/lw`, or a custom location explicitly
selected with `LW_INSTALL_DIR=/custom/bin`. An unrelated `lw` elsewhere on PATH
is never executed or overwritten; the script asks you to name its directory
explicitly. If multiple distinct installations are found, it stops and lists
them rather than guessing which one to replace.

Updates intentionally do not restart active VM/container daemons because that
could interrupt a job. They load the new binary on their next normal restart.
Services installed by this version record a stable PATH-level executable; if
an older Homebrew service captured a versioned Cellar path, reinstall that
watchdog or isolated runner during an idle maintenance window.

State lives in `~/.local-worker/` (runner instances, logs, config). The
repo itself stays clean — clone it on as many machines as you like.

Each attached repo gets one runner, and a runner executes one job at a time —
so different repos run in parallel, while jobs within a repo queue (usually
what you want for deploys). Multiple runners per repo isn't supported yet.

## Routing jobs to your Mac

Minimal change in any workflow:

```yaml
jobs:
  build:
    runs-on: [self-hosted, macOS]
```

`lw integrate` prints a fancier pattern with a `workflow_dispatch` input that
falls back to GitHub-hosted runners when your Mac is down:

```yaml
runs-on: ${{ inputs.runner || 'self-hosted' }}
```

Queued jobs wait up to 24 h for a runner to come back — a short home-internet
outage just delays the job, it doesn't fail it.

### Let an AI agent do the migration

The repo ships an agent skill, [`skills/lw-adapt`](skills/lw-adapt/SKILL.md),
that walks Claude Code (or any agent that reads skills) through adapting an
existing repository: preflight checks, patching every workflow with the
fallback pattern, arch/tooling pitfalls, and end-to-end verification.

```bash
# make it available in all your projects
cp -r skills/lw-adapt ~/.claude/skills/
```

Then, inside the repo you want to migrate: `/lw-adapt`.

## Staying alive 24/7

`lw setup` offers to apply these; `lw doctor` verifies them:

- `sudo pmset -a sleep 0 disksleep 0 autorestart 1 womp 1`
- `sudo pmset -a disablesleep 1` — keeps a MacBook awake with the lid closed
- **Automatic login** (System Settings → Users & Groups) so runners start
  after a reboot without a keyboard. FileVault blocks auto-login; on a
  dedicated CI Mac, consider disabling it.
- The **watchdog** (`lw watchdog install`) restarts dead runner processes and
  pings a [healthchecks.io](https://healthchecks.io) URL every 5 minutes — if
  the machine drops off the internet, the missed pings alert you by
  email/Telegram. For direct Telegram messages when the watchdog restarts a
  runner, put a bot token + chat id into `~/.local-worker/config.json` under
  `watchdog.telegram: { "botToken": "…", "chatId": "…" }`.

## Security notes

- Runners run as your user, unsandboxed by default (that's what makes
  Xcode/simulator builds work). Dedicate a user account — or the whole
  machine — to CI, or add walls with [Isolation](#isolation) below.
- Repo secrets pass through this machine during jobs. Treat it like a
  production box: screen lock for humans, no random software.
- Public repos are a special case — see
  [Running public repos](#running-public-repos-read-this-first).

## Isolation

By default runners execute jobs directly on the host (`--isolation host`) —
fastest, full toolchain access, right for trusted code on a dedicated
machine. When you want walls, local-worker can run the runner inside a
disposable environment instead:

```bash
lw add owner/repo --isolation vm                       # Tart macOS VM (Apple Silicon hosts)
lw add owner/repo --isolation container                # Docker container (Linux hosts, or macOS via OrbStack/Colima)
```

How often the environment is thrown away and rebuilt from its base image is a
separate dial — pick the trade-off you want:

| `--refresh` | Behaviour | Protects against | Cost |
| --- | --- | --- | --- |
| `never` (default) | one environment, reused forever | host compromise | caches persist — fastest |
| `24h` (any `<n>h`) | auto-rebuilt on an interval | host compromise + limits how long an implant can live | cache lost on each rebuild |
| `per-job` | fresh clone per job, discarded after | host compromise + **cross-job persistence** | cold caches + boot time per job |

`lw vm status` shows environment age and job counts; `lw vm refresh owner/repo`
discards one immediately (a clean clone comes back on its own).

**Why per-job matters:** isolation stops a malicious job from touching your
machine, but in a *reused* environment it can still plant a backdoor or poison
caches, then quietly harvest the secrets of every **later** job (deploy keys,
registry tokens) — or inject code into artifacts you ship. Throwing the
environment away after each job caps the blast radius at that one job.

Choosing:
- **Own private repos, dedicated machine** → `host`, or a dedicated non-admin
  `ci` user account. Isolation buys you little here; speed matters more.
- **Semi-trusted code / peace of mind** → `vm`/`container` with `never` or an
  interval — host is protected, caches mostly survive.
- **Anything public or untrusted** → `per-job`, no exceptions.

Notes: VM isolation needs [Tart](https://tart.run) (`brew install
cirruslabs/cli/tart`) + `sshpass`, an Apple Silicon Mac, and disk for base
images (Ubuntu ~2 GB, macOS ~25 GB, macOS+Xcode ~60 GB). Container isolation
uses GitHub's official runner image by default; jobs inside it cannot use
Docker themselves (mounting the host socket would defeat the isolation).
macOS VMs: Apple licensing allows 2 concurrent VMs per host.

> **Maturity:** the `container` backend is tested end-to-end (including the
> per-job discard/rebuild cycle). The `vm` (Tart) backend shares the same
> daemon code but has **not yet been exercised on real hardware** — treat it
> as experimental and please report what you hit.

## Running public repos (read this first)

A self-hosted runner executes whatever workflow code reaches it. On a public
repo, that can include a stranger's fork PR. If you must attach one, treat it
as hostile input — all of the following, together:

1. **Settings → Actions**: require approval for **all outside collaborators**
   (the default only gates *first-time* contributors — one merged typo fix
   and they run unreviewed), and keep fork PRs away from self-hosted jobs.
2. **`--isolation vm|container --refresh per-job`** so nothing survives
   between jobs.
3. Remember fork PRs get no repo secrets and a read-only token — keep it that
   way; never add `pull_request_target` workflows that expose secrets to
   fork-controlled code.

`lw add` warns and asks for explicit confirmation before attaching a public
repo. Private repos: the walls still help (a compromised dependency in your
own build is the same attack), but you choose the speed/paranoia trade-off.

## Building linux/amd64 Docker images on Apple Silicon

Deploying to an x86 server? Two options that avoid slow emulation:

1. **COPY-only Dockerfiles** — build artifacts on the Mac natively, let the
   image just package them. For Node projects, pnpm's `supportedArchitectures`
   (`{os: ["linux"], cpu: ["x64"]}`) installs target-platform deps on the Mac.
2. **Rosetta-backed builds** — OrbStack / Docker Desktop / Colima run amd64
   layers under Rosetta 2, close to native speed for install/copy workloads.

## Troubleshooting

**Job stuck in "Queued"** — no online runner matches the job's labels. Check
`lw status` (is the runner online?) and the workflow's `runs-on` (a
`[self-hosted, macOS]` array must match ALL labels — your runner's are shown
by `lw status`). Queued jobs auto-cancel after 24 h.

**Runner offline after a reboot** — macOS: automatic login is off (services
start at login) or FileVault is waiting for a password. Linux: lingering is
off (`loginctl enable-linger $USER`). `lw doctor` checks both.

**`gh: Requires authentication` in a daemon log** — the service can't reach
gh's keyring; re-run `lw add … --isolation …` to refresh the injected token
(also needed if you revoke/rotate your GitHub token).

**Isolated runner never comes online** — first start downloads the base image
(up to 60 GB for macOS+Xcode); watch `lw logs owner/repo`. Also verify the
backend prerequisites: `docker info` works, or `tart --version` + `sshpass`.

**Every command asks about an unsupported platform** — Windows outside WSL2
isn't supported; see Platform support above.

## Contributing

Issues and PRs are welcome. Note for contributors: maintainers run this repo's
CI on hosted runners — fork PRs never reach anyone's personal machine.

## Uninstall

Remove runner and watchdog services before deleting local state:

```bash
lw remove owner/repo        # repeat for each repo
lw watchdog uninstall
rm -rf ~/.local-worker
```

Then remove the installation through the channel that owns it:

```bash
rm -f ~/.local/bin/lw       # direct/script install
brew uninstall local-worker # Homebrew install
```
