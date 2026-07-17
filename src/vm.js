// Tart-based VM isolation (macOS Apple Silicon hosts only).
//
// One daemon per repo owns the VM lifecycle and unifies every refresh policy:
//
//   loop {
//     recreate VM from base image if missing or refresh policy says it's due
//     boot it, wait for SSH
//     provision the actions runner inside (idempotent)
//     register (ephemeral for per-job policy) and block on run.sh
//     count the job; per-job policy deletes the VM here
//   }
//
// The daemon itself runs under the same service manager as plain runners, so
// KeepAlive/Restart=always revives it and `lw remove` tears it down.
import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import { ROOT, sh, trySh, ghJson, repoId, timestamp } from "./util.js";
import { latestRunnerAsset, registrationToken, removalToken } from "./runner.js";
import { loadConfig, findRunner } from "./config.js";

const VM_STATE_DIR = path.join(ROOT, "vm");
export const DEFAULT_IMAGES = {
  "macos-base": "ghcr.io/cirruslabs/macos-sequoia-base:latest",
  "macos-xcode": "ghcr.io/cirruslabs/macos-sequoia-xcode:latest",
  ubuntu: "ghcr.io/cirruslabs/ubuntu:latest",
};
const SSH_USER = "admin"; // cirruslabs images ship admin/admin
const SSH_PASS = "admin";

export const vmName = (repo) => `lw-${repoId(repo).toLowerCase()}`;

export function tartAvailable() {
  return !!trySh("tart", ["--version"]);
}

export function sshpassAvailable() {
  return !!trySh("sshpass", ["-V"]);
}

export function assertVmSupport() {
  if (process.platform !== "darwin" || process.arch !== "arm64") {
    throw new Error("VM isolation needs an Apple Silicon Mac (Tart).");
  }
  if (!tartAvailable()) {
    throw new Error("Tart is not installed — brew install cirruslabs/cli/tart");
  }
  if (!sshpassAvailable()) {
    throw new Error("sshpass is not installed — brew install sshpass");
  }
}

// ---------------------------------------------------------------- tart layer

function tartList() {
  const out = trySh("tart", ["list", "--format", "json"]);
  return out ? JSON.parse(out) : [];
}

export function vmExists(name) {
  return tartList().some((v) => v.Name === name);
}

export function vmRunning(name) {
  return tartList().some((v) => v.Name === name && v.State === "running");
}

export function deleteVm(name) {
  if (!vmExists(name)) return;
  if (vmRunning(name)) trySh("tart", ["stop", name, "--timeout", "30"]);
  trySh("tart", ["delete", name]);
}

function cloneVm(name, image) {
  // Pulls the base image on first use — can be 2 GB (Linux) to 60+ GB (Xcode).
  sh("tart", ["clone", image, name], { stdio: ["ignore", "inherit", "inherit"] });
}

function bootVm(name) {
  const child = spawn("tart", ["run", name, "--no-graphics"], {
    detached: true,
    stdio: "ignore",
  });
  child.unref();
}

// ----------------------------------------------------------------- ssh layer

function sshArgs(ip, cmd) {
  return [
    "-p", SSH_PASS, "ssh",
    "-o", "StrictHostKeyChecking=no",
    "-o", "UserKnownHostsFile=/dev/null",
    "-o", "LogLevel=ERROR",
    "-o", "ConnectTimeout=10",
    `${SSH_USER}@${ip}`,
    cmd,
  ];
}

function sshTry(ip, cmd) {
  return trySh("sshpass", sshArgs(ip, cmd));
}

function sshRun(ip, cmd) {
  // Blocking, output to the daemon log.
  return sh("sshpass", sshArgs(ip, cmd), {
    stdio: ["ignore", "inherit", "inherit"],
    maxBuffer: 64 * 1024 * 1024,
  });
}

async function waitForSsh(name, log) {
  for (let i = 0; i < 60; i++) {
    const ip = trySh("tart", ["ip", name]);
    if (ip && sshTry(ip, "true") !== null) return ip;
    if (i === 10) log(`still waiting for ${name} to become reachable…`);
    await sleep(5000);
  }
  throw new Error(`VM ${name} did not become reachable over SSH`);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// --------------------------------------------------------------- state layer

function statePath(name) {
  return path.join(VM_STATE_DIR, `${name}.json`);
}

export function readVmState(name) {
  try {
    return JSON.parse(fs.readFileSync(statePath(name), "utf8"));
  } catch {
    return null;
  }
}

function writeVmState(name, state) {
  fs.mkdirSync(VM_STATE_DIR, { recursive: true });
  fs.writeFileSync(statePath(name), JSON.stringify(state, null, 2) + "\n");
}

export function clearVmState(name) {
  fs.rmSync(statePath(name), { force: true });
}

/** "never" | "per-job" | "<n>h" — is a scheduled rebuild due? */
function refreshDue(policy, state) {
  if (!state) return false;
  const hours = parseRefreshHours(policy);
  if (hours === null) return false;
  return Date.now() - Date.parse(state.createdAt) > hours * 3600_000;
}

export function parseRefreshHours(policy) {
  const m = /^(\d+(?:\.\d+)?)h$/.exec(policy || "");
  return m ? Number(m[1]) : null;
}

export function validRefresh(policy) {
  return policy === "never" || policy === "per-job" || parseRefreshHours(policy) !== null;
}

// -------------------------------------------------------------- provisioning

/** Install the runner inside the VM (idempotent) and configure it. */
function provisionRunner(ip, { repo, name, labels, ephemeral }) {
  const unameOut = sshTry(ip, "uname -sm") || "";
  const [os, cpu] = unameOut.split(/\s+/);
  const slug = `${os === "Darwin" ? "osx" : "linux"}-${/arm|aarch/.test(cpu) ? "arm64" : "x64"}`;
  const asset = latestRunnerAsset(slug);

  sshRun(
    ip,
    `set -e; mkdir -p runner; cd runner; ` +
      `[ -x config.sh ] || { curl -fsSL -o r.tgz "${asset.url}"; tar xzf r.tgz; rm r.tgz; }`
  );

  // Persistent VMs stay configured across daemon restarts; ephemeral runners
  // deregister themselves after each job, so configure every cycle.
  const already = sshTry(ip, "test -f runner/.runner && echo yes");
  if (already === "yes") {
    if (!ephemeral) return;
    // A locally-configured runner refuses reconfiguration — unconfigure
    // first, force-cleaning if the graceful remove fails.
    let rt = null;
    try { rt = removalToken(repo); } catch { /* offline — force-clean below */ }
    if (rt) sshTry(ip, `cd runner && ./config.sh remove --token "${rt}"`);
    sshTry(ip, "cd runner && rm -f .runner .credentials .credentials_rsaparams");
  }

  const token = registrationToken(repo);
  sshRun(
    ip,
    `cd runner && ./config.sh --url "https://github.com/${repo}" --token "${token}" ` +
      `--name "${name}" --labels "${labels.join(",")}" --work _work --unattended --replace` +
      (ephemeral ? " --ephemeral" : "")
  );
}

// -------------------------------------------------------------------- daemon

/** Long-running per-repo VM daemon; stdout/stderr go to the service log. */
export async function vmDaemon(repo) {
  const log = (msg) => console.log(`[${timestamp()}] ${msg}`);
  assertVmSupport();

  for (;;) {
    try {
      const entry = findRunner(loadConfig(), repo);
      if (!entry?.vm) {
        log(`${repo} no longer configured for VM isolation — exiting.`);
        return;
      }
      const { image, refresh } = entry.vm;
      const name = vmName(repo);
      let state = readVmState(name);

      if (!vmExists(name) || refreshDue(refresh, state) || state?.recreateRequested) {
        log(`(re)creating VM ${name} from ${image}`);
        deleteVm(name);
        cloneVm(name, image);
        state = { image, createdAt: timestamp(), jobsRun: 0 };
        writeVmState(name, state);
      }

      if (!state) {
        // Adopt a pre-existing environment (e.g. state file lost).
        state = { image, createdAt: timestamp(), jobsRun: 0 };
        writeVmState(name, state);
      }
      if (!vmRunning(name)) {
        log(`booting ${name}`);
        bootVm(name);
      }
      const ip = await waitForSsh(name, log);

      // A previous daemon's run.sh may still be alive inside the VM (a broken
      // ssh session doesn't kill it) — stop it before configuring, or
      // config.sh refuses to touch a running runner.
      sshTry(ip, "pkill -f Runner.Listener");

      const ephemeral = refresh === "per-job";
      provisionRunner(ip, {
        repo,
        name: entry.name,
        labels: [...entry.labels, "vm"],
        ephemeral,
      });

      log(`runner up in ${name} (${ip}), waiting for jobs${ephemeral ? " (ephemeral)" : ""}`);
      sshRun(ip, "cd runner && ./run.sh");

      state = readVmState(name) || state || { image, createdAt: timestamp(), jobsRun: 0 };
      state.jobsRun = (state.jobsRun || 0) + 1;
      writeVmState(name, state);

      if (ephemeral) {
        log(`job finished — discarding ${name}`);
        deleteVm(name);
      }
    } catch (err) {
      log(`daemon error: ${err.message}`);
      await sleep(30_000); // don't spin on persistent failures
    }
  }
}

/** Force-deregister a VM runner from GitHub by name (the VM may be gone). */
export function forceDeregister(repo, runnerName) {
  try {
    const runners = ghJson([`repos/${repo}/actions/runners`])?.runners ?? [];
    const mine = runners.find((r) => r.name === runnerName);
    if (mine) sh("gh", ["api", "-X", "DELETE", `repos/${repo}/actions/runners/${mine.id}`]);
  } catch {
    // Repo gone or offline — nothing to clean remotely.
  }
}
