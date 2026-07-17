---
name: lw-adapt
description: Adapt an existing repository's GitHub Actions workflows to run on a self-hosted local-worker (lw) runner. Use when the user wants this repo's CI jobs routed to their own machine, wants to migrate off paid hosted runners, or mentions local-worker / lw / self-hosted runner integration.
---

# Adapt this repository to local-worker

You are migrating the current repository's GitHub Actions workflows so jobs run
on the user's own machine (a `local-worker` self-hosted runner) instead of
GitHub-hosted runners — without losing the ability to fall back to hosted
runners when the machine is down.

## 1. Preflight

1. `lw version` — if `lw` is missing, stop and tell the user to install it
   (`brew tap ssukru/local-worker https://github.com/ssukru/local-worker.git && brew install local-worker`,
   or the repo's `install.sh`). Note: `lw` must run on the machine that will
   execute the jobs; if this session is not on that machine, skip runner
   checks and only patch workflows.
2. Resolve the repo slug: `gh repo view --json nameWithOwner -q .nameWithOwner`.
3. Check a runner is attached and online for this repo:
   `gh api repos/<owner>/<repo>/actions/runners --jq '.runners[] | {name, status, labels: [.labels[].name]}'`.
   - No runner and you ARE on the worker machine → run `lw add <owner>/<repo>`
     (ask before doing this; it registers the machine).
   - No runner and you are NOT on the worker machine → tell the user to run
     `lw add <owner>/<repo>` there, and continue with the workflow patches.
4. **Public repo?** (`gh repo view --json isPrivate`) Stop and warn before
   anything else: self-hosted runners on public repos can be attacked via fork
   PRs. Require (a) Settings → Actions approval for ALL outside collaborators,
   and (b) `--isolation vm|container --refresh per-job` on the runner. Do not
   route `pull_request` jobs from forks to the runner at all.

## 2. Inventory the workflows

Read every file in `.github/workflows/`. For each job record: `runs-on`,
`container:`, `services:`, matrix `os` entries, and what the job does
(build/test/deploy/release).

Good candidates: build, test, deploy jobs on `ubuntu-latest` / `macos-*`.
Leave alone: `windows-*` jobs, fork-facing `pull_request` jobs on public
repos, and jobs that need tools the worker machine lacks (check with
`lw doctor` / ask the user).

Note the runner's actual labels (from step 1.3, typically
`self-hosted, macOS, ARM64, local-worker`) — a `runs-on` array must match ALL
of its labels.

## 3. Apply the routing pattern

Preferred pattern — hosted fallback via a `workflow_dispatch` input (safe to
add alongside existing inputs; on `push` events inputs are empty so the `||`
fallback picks the runner):

```yaml
on:
  workflow_dispatch:
    inputs:
      runner:
        description: Where to run this job
        type: choice
        default: self-hosted
        options:
          - self-hosted
          - ubuntu-latest   # use macos-15 as the fallback for iOS/macOS jobs

jobs:
  build:
    runs-on: ${{ inputs.runner || 'self-hosted' }}
```

When strict label matching matters more than the fallback trick, use an array
instead (the two can't be combined in one `runs-on`):

```yaml
runs-on: [self-hosted, macOS]
```

`lw integrate <owner>/<repo>` prints this patch too — keep your edits
consistent with it.

## 4. Check the pitfalls that actually bite

- **Architecture**: an Apple Silicon runner is `arm64`. Jobs that download
  x86 binaries, build `linux/amd64` Docker images, or assume `ubuntu` will
  need attention. For amd64 images on arm64: prefer COPY-only Dockerfiles
  (build artifacts natively, image only packages them; for Node, pnpm
  `supportedArchitectures: {os: [linux], cpu: [x64]}`) or Rosetta-backed
  builds (OrbStack/Colima/Docker Desktop).
- **Preinstalled tools**: hosted runners ship hundreds of tools; the worker
  machine has only what's installed on it. Make jobs install what they need,
  or verify the machine has it.
- **State persists** between jobs on `--isolation host`: workspaces, caches
  and global tool state carry over. Don't rely on a clean machine; keep
  `actions/checkout` (it cleans the workspace) and treat leftover state as a
  feature (warm caches) or add `clean: true` where it isn't.
- **Concurrency**: one runner = one job at a time per repo; parallel matrix
  jobs will queue. Usually fine for deploys, may slow big test matrices.
- **Docker `services:`/`container:`** need a Docker daemon on the worker
  machine (`docker info`) — on macOS that means OrbStack/Colima/Docker
  Desktop running at login.
- Secrets, environments, `GITHUB_TOKEN`, and cache actions keep working
  unchanged.

## 5. Verify

1. Commit the workflow changes on a branch and push.
2. Trigger: `gh workflow run <workflow> --ref <branch>` (add
   `-f runner=self-hosted` if the input was added) — or let a push event fire.
3. Watch: `gh run watch` and confirm the job ran on the runner: the run's job
   page shows the runner name, or `lw status` shows `online · running a job`.
4. Test the fallback path once: dispatch with `-f runner=ubuntu-latest` (or
   the chosen hosted fallback) and confirm it still passes.
5. If a job sits in "Queued": no online runner matches its labels — compare
   `runs-on` against the labels from step 1.3 and check `lw status`.
