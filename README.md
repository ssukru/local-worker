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

## Contributing

Issues and PRs are welcome. Note for contributors: maintainers run this repo's
CI on hosted runners — fork PRs never reach anyone's personal machine.

## Uninstall

```bash
lw remove owner/repo        # per repo
lw watchdog uninstall
rm -rf ~/.local-worker
```
