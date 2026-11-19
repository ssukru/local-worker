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

`lw` is a single static Go binary — no runtime dependencies beyond `git` and
the GitHub CLI (`gh`, which `lw setup` installs if missing).

**Homebrew** (macOS & Linux):

```bash
brew tap ssukru/local-worker https://github.com/ssukru/local-worker.git
brew trust ssukru/local-worker   # newer Homebrew requires trusting third-party taps
brew install local-worker
```

**Install script** (downloads the latest release binary):

```bash
curl -fsSL https://raw.githubusercontent.com/ssukru/local-worker/main/install.sh | sh
```

**From source** (needs Go 1.23+):

```bash
git clone https://github.com/ssukru/local-worker.git
cd local-worker && go build -o /usr/local/bin/lw ./cmd/lw
```

Then run `lw setup`. The guided wizard signs you into GitHub (browser device-flow — no tokens to
copy around), checks the machine (power settings, disk, Docker, Xcode), lets
you pick which repos send jobs here, and installs everything as launchd
services that survive reboots.

## Commands

| Command | What it does |
| --- | --- |
| `lw setup` | Guided first-time setup |
| `lw add owner/repo` | Attach this machine as a runner for a repo (autoscales) |
| `lw remove owner/repo` | Deregister + clean up |
| `lw scale owner/repo [max]` | Resize an autoscaling pool (`--min` / `--max`) |
| `lw status` | Local *and* GitHub's view of every runner |
| `lw doctor` | Diagnose machine readiness (sleep, auto-login, tools…) |
| `lw watchdog install\|uninstall\|check\|status` | Self-healing checks + offline alerting |
| `lw vm status\|refresh owner/repo` | Inspect / rebuild isolated environments |
| `lw integrate owner/repo` | Copy-paste workflow patch to route jobs here |
| `lw logs [owner/repo] [--slot N]` | Tail a runner's log |

`lw add` flags: `--min N` · `--max N` · `--count N` · `--labels a,b,c` · `--name <runner-name>` ·
`--isolation host|vm|container` · `--vm-image <image>` ·
`--refresh never|per-job|<n>h` · `--yes` (non-interactive).

State lives in `~/.local-worker/` (runner instances, logs, config). The
repo itself stays clean — clone it on as many machines as you like.

## Concurrency: it scales itself

A GitHub Actions runner executes **one job at a time**. Rather than making you
size a runner pool by hand, `lw` does what GitHub-hosted runners appear to do:

```bash
lw add owner/repo     # that's it — autoscaling is the default
```

An autoscaler daemon watches the repo's job queue and keeps **one ephemeral
runner per queued job**. Trigger 6 jobs and 6 runners start; when the queue
drains they retire themselves. Nothing to configure per workflow, and no
runner count to keep in sync with your matrix size.

```
queue:  ██████ 6 jobs
pool:   idle(1) → starts 5 more → 6 running → jobs finish → back to idle(1)
```

Each runner registers with `--ephemeral`, so it accepts exactly one job and
deregisters itself. Scaling down leaves nothing stale behind on GitHub.

### Bounds

| | default | meaning |
| --- | --- | --- |
| `--min` | `1` | runners kept warm, so a single job starts instantly |
| `--max` | machine-sized | ceiling on parallel jobs |

The default ceiling is derived from this machine — cores ÷ 2, capped by a
~4 GB-per-job memory budget, because CI jobs hit RAM before they hit cores.
Check it with `lw status`, change it any time:

```bash
lw scale owner/repo --max 10    # raise the ceiling
lw scale owner/repo --min 0     # fully cold: no idle runners at all
lw scale owner/repo             # just show the current pool
```

`lw scale` takes effect within seconds — the daemon re-reads its config every
poll. Nothing restarts and jobs in flight are never interrupted.

### Fixed pools

When you want an exact number instead — deploy repos are the usual case, so
two deploys can't race — pin it:

```bash
lw add owner/repo --count 1     # exactly one runner, no autoscaling
lw add owner/repo --count 3     # exactly three
```

Fixed pools are permanent registrations with one service each, which is also
what every repo attached before autoscaling existed keeps using. Upgrading
`lw` never silently converts them; re-run `lw add` with `--min`/`--max` to opt
a repo in.

### What to watch out for

Parallel jobs share one machine, so raising `--max` isn't free:

- **RAM** is usually the first wall — N concurrent Node/Xcode builds means N
  times the memory.
- **Disk**: each runner keeps its own checkout and runner copy (~200 MB before
  your dependencies).
- **Shared host state.** On `--isolation host` parallel jobs collide on fixed
  ports, one Docker daemon, and package-manager caches. Most caches tolerate
  concurrent access; fixed ports do not. If your jobs bind ports, use
  `--isolation container`.
- **Autoscaling is host-only.** VM/container isolation uses fixed pools —
  booting a fresh VM per job is too slow to scale on demand.

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

```bash
lw remove owner/repo        # per repo
lw watchdog uninstall
rm -rf ~/.local-worker
```
