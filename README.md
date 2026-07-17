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
(`[boot]` → `systemd=true`), then clone this repo inside WSL and run
`./install.sh`. Your jobs run in Linux — the same environment as GitHub's
`ubuntu-*` hosted runners.

## Install (on the machine that will do the work)

```bash
git clone https://github.com/ssukru/local-worker.git
cd local-worker
./install.sh        # installs brew/node/gh if missing, then runs `lw setup`
```

The guided wizard signs you into GitHub (browser device-flow — no tokens to
copy around), checks the machine (power settings, disk, Docker, Xcode), lets
you pick which repos send jobs here, and installs everything as launchd
services that survive reboots.

## Commands

| Command | What it does |
| --- | --- |
| `lw setup` | Guided first-time setup |
| `lw add owner/repo` | Attach this Mac as a runner for a repo |
| `lw remove owner/repo` | Deregister + clean up |
| `lw status` | Local *and* GitHub's view of every runner |
| `lw doctor` | Diagnose machine readiness (sleep, auto-login, tools…) |
| `lw watchdog install` | Self-healing checks + offline alerting |
| `lw integrate owner/repo` | Copy-paste workflow patch to route jobs here |
| `lw logs [owner/repo]` | Tail a runner's log |

State lives in `~/.local-worker/` (runner instances, logs, config). The
repo itself stays clean — clone it on as many Macs as you like.

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

## Staying alive 24/7

`lw setup` offers to apply these; `lw doctor` verifies them:

- `sudo pmset -a sleep 0 disksleep 0 autorestart 1 womp 1`
- `sudo pmset -a disablesleep 1` — keeps a MacBook awake with the lid closed
- **Automatic login** (System Settings → Users & Groups) so runners start
  after a reboot without a keyboard. FileVault blocks auto-login; on a
  dedicated CI Mac, consider disabling it.
- The **watchdog** (`lw watchdog install`) restarts dead runner processes and
  pings a [healthchecks.io](https://healthchecks.io) URL every 5 minutes — if
  the Mac drops off the internet, the missed pings alert you by email/Telegram.

## Security notes

- **Private repos only**, ideally. A self-hosted runner executes whatever the
  workflow tells it to; on public repos, fork PRs are a code-execution vector.
  `lw add` warns loudly and makes you confirm before attaching a public repo.
- Runners run as your user, unsandboxed (that's what makes Xcode/simulator
  builds work). Dedicate a user account — or the whole machine — to CI.
- Repo secrets pass through this machine during jobs. Treat it like a
  production box: screen lock for humans, no random software.

### Isolation options, weakest to strongest

1. **Dedicated user account** (recommended minimum): create a standard,
   non-admin `ci` user, log in as it, run `./install.sh` there. Jobs can't
   read your personal files or keychain, and `sudo` is off the table.
2. **Dedicated machine**: the spare-Mac scenario this tool is built for —
   nothing personal lives on the box at all.
3. **Ephemeral VMs** (macOS): [Tart](https://tart.run) boots a throwaway
   macOS VM per job — the strongest isolation, at the cost of disk, RAM and
   per-job VM boot time. Not integrated into local-worker (yet); if your
   repos run untrusted code, start there.
4. On **Linux**, running the runner inside a Docker container gives similar
   per-job disposability cheaply.

For a personal Mac running jobs from your *own* private repos, option 1 or 2
is the sensible trade-off.

## Building linux/amd64 Docker images on Apple Silicon

Deploying to an x86 server? Two options that avoid slow emulation:

1. **COPY-only Dockerfiles** — build artifacts on the Mac natively, let the
   image just package them. For Node projects, pnpm's `supportedArchitectures`
   (`{os: ["linux"], cpu: ["x64"]}`) installs target-platform deps on the Mac.
2. **Rosetta-backed builds** — OrbStack / Docker Desktop / Colima run amd64
   layers under Rosetta 2, close to native speed for install/copy workloads.

## Contributing

Issues and PRs are welcome. Note for contributors: maintainers run this repo's
CI on hosted runners — fork PRs never reach anyone's personal machine.

## Uninstall

```bash
lw remove owner/repo        # per repo
lw watchdog uninstall
rm -rf ~/.local-worker
```
