// Docker-based isolation backend — the Linux counterpart of Tart VMs (also
// usable on macOS hosts that have a Docker daemon, for cheap Linux runners).
//
// Same refresh policies as VMs: "never" | "per-job" | "<n>h". The daemon loop
// mirrors src/vm.js but speaks `docker` instead of `tart`+ssh.
//
// Default image is GitHub's official runner image (runner preinstalled under
// /home/runner). Jobs inside the container cannot run Docker themselves
// unless you opt into mounting the host socket — which largely defeats the
// isolation, so we don't.
import fs from "node:fs";
import path from "node:path";
import { ROOT, sh, trySh, repoId, timestamp } from "./util.js";
import { registrationToken, removalToken } from "./runner.js";
import { loadConfig, findRunner } from "./config.js";

export const DEFAULT_CONTAINER_IMAGE = "ghcr.io/actions/actions-runner:latest";
const STATE_DIR = path.join(ROOT, "vm"); // shared state dir with VM backend

export const containerName = (repo) => `lw-${repoId(repo).toLowerCase()}`;

export function dockerAvailable() {
  return !!trySh("docker", ["info", "--format", "ok"]);
}

export function assertContainerSupport() {
  if (!dockerAvailable()) {
    throw new Error(
      "Container isolation needs a running Docker daemon (Linux: https://get.docker.com, macOS: OrbStack/Colima)."
    );
  }
}

function containerExists(name) {
  return trySh("docker", ["inspect", "--format", "ok", name]) === "ok";
}

function containerRunning(name) {
  return trySh("docker", ["inspect", "--format", "{{.State.Running}}", name]) === "true";
}

export function deleteContainer(name) {
  trySh("docker", ["rm", "-f", name]);
}

function createContainer(name, image) {
  sh("docker", ["run", "-d", "--name", name, image, "tail", "-f", "/dev/null"], {
    stdio: ["ignore", "inherit", "inherit"],
  });
}

function execTry(name, cmd) {
  return trySh("docker", ["exec", "-u", "runner", "-w", "/home/runner", name, "bash", "-c", cmd]);
}

function execRun(name, cmd) {
  return sh("docker", ["exec", "-u", "runner", "-w", "/home/runner", name, "bash", "-c", cmd], {
    stdio: ["ignore", "inherit", "inherit"],
    maxBuffer: 64 * 1024 * 1024,
  });
}

function statePath(name) {
  return path.join(STATE_DIR, `${name}.json`);
}

function readState(name) {
  try {
    return JSON.parse(fs.readFileSync(statePath(name), "utf8"));
  } catch {
    return null;
  }
}

function writeState(name, state) {
  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(statePath(name), JSON.stringify(state, null, 2) + "\n");
}

function refreshDue(policy, state) {
  const m = /^(\d+(?:\.\d+)?)h$/.exec(policy || "");
  if (!m || !state) return false;
  return Date.now() - Date.parse(state.createdAt) > Number(m[1]) * 3600_000;
}

function configureRunnerInContainer(name, { repo, runnerName, labels, ephemeral }) {
  const already = execTry(name, "test -f .runner && echo yes");
  if (already === "yes") {
    if (!ephemeral) return;
    // A locally-configured runner refuses reconfiguration (--replace only
    // covers the server side) — unconfigure first, force-cleaning if the
    // graceful remove fails.
    let rt = null;
    try { rt = removalToken(repo); } catch { /* offline — force-clean below */ }
    if (rt) execTry(name, `./config.sh remove --token "${rt}"`);
    execTry(name, "rm -f .runner .credentials .credentials_rsaparams");
  }
  const token = registrationToken(repo);
  execRun(
    name,
    `./config.sh --url "https://github.com/${repo}" --token "${token}" ` +
      `--name "${runnerName}" --labels "${labels.join(",")}" --work _work --unattended --replace` +
      (ephemeral ? " --ephemeral" : "")
  );
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Long-running per-repo container daemon; mirrors vmDaemon. */
export async function containerDaemon(repo) {
  const log = (msg) => console.log(`[${timestamp()}] ${msg}`);
  assertContainerSupport();

  for (;;) {
    try {
      const entry = findRunner(loadConfig(), repo);
      if (!entry?.vm) {
        log(`${repo} no longer configured for container isolation — exiting.`);
        return;
      }
      const { image, refresh } = entry.vm;
      const name = containerName(repo);
      let state = readState(name);

      if (!containerExists(name) || refreshDue(refresh, state) || state?.recreateRequested) {
        log(`(re)creating container ${name} from ${image}`);
        deleteContainer(name);
        createContainer(name, image);
        state = { image, createdAt: timestamp(), jobsRun: 0 };
        writeState(name, state);
      }
      if (!state) {
        // Adopt a pre-existing environment (e.g. state file lost).
        state = { image, createdAt: timestamp(), jobsRun: 0 };
        writeState(name, state);
      }
      if (!containerRunning(name)) {
        sh("docker", ["start", name]);
      }

      // A previous daemon's run.sh may still be alive inside the container
      // (killing the docker exec client doesn't kill it) — stop it before
      // configuring, or config.sh refuses to touch a running runner.
      execTry(name, "pkill -f Runner.Listener");

      const ephemeral = refresh === "per-job";
      configureRunnerInContainer(name, {
        repo,
        runnerName: entry.name,
        labels: [...entry.labels, "container"],
        ephemeral,
      });

      log(`runner up in container ${name}, waiting for jobs${ephemeral ? " (ephemeral)" : ""}`);
      execRun(name, "./run.sh");

      state = readState(name) || state || { image, createdAt: timestamp(), jobsRun: 0 };
      state.jobsRun = (state.jobsRun || 0) + 1;
      writeState(name, state);

      if (ephemeral) {
        log(`job finished — discarding ${name}`);
        deleteContainer(name);
      }
    } catch (err) {
      log(`daemon error: ${err.message}`);
      await sleep(30_000);
    }
  }
}
