import * as p from "@clack/prompts";
import pc from "picocolors";
import fs from "node:fs";
import path from "node:path";
import { loadConfig, findRunner } from "../config.js";
import { ROOT, runnerLabel, timestamp } from "../util.js";
import { svc } from "../service/index.js";
import { vmDaemon, vmName, vmExists, vmRunning, readVmState } from "../vm.js";
import { containerDaemon, containerName } from "../container.js";

export async function vm(sub, repoArg, flags = {}) {
  switch (sub) {
    case "daemon": return daemon(repoArg); // internal: run by the service manager
    case "status": return status();
    case "refresh": return refresh(repoArg);
    default:
      console.log(
        "Usage: lw vm <status|refresh owner/repo>\n" +
          "  status            Show isolated runners (VMs / containers)\n" +
          "  refresh           Discard an isolated environment now; a clean one is rebuilt"
      );
  }
}

async function daemon(repo) {
  if (!repo) throw new Error("Usage: lw vm daemon owner/repo");
  const entry = findRunner(loadConfig(), repo);
  if (!entry) throw new Error(`${repo} is not configured`);
  if (entry.isolation === "container") return containerDaemon(repo);
  return vmDaemon(repo);
}

function isolatedRunners() {
  return loadConfig().runners.filter((r) => r.isolation && r.isolation !== "host");
}

async function status() {
  p.intro(pc.bgCyan(pc.black(" local-worker · vm status ")));
  const entries = isolatedRunners();
  if (!entries.length) {
    p.log.info("No isolated runners. Attach one with: lw add owner/repo --isolation vm|container");
    p.outro("");
    return;
  }
  for (const r of entries) {
    const name = r.isolation === "container" ? containerName(r.repo) : vmName(r.repo);
    const state = readVmState(name);
    const daemonUp = svc.isLoaded(runnerLabel(r.repo));
    const envUp =
      r.isolation === "container"
        ? // containerRunning without importing private fn: state daemon reports via docker
          undefined
        : vmRunning(name);
    p.log.message(
      `${pc.bold(r.repo)}\n` +
        `  isolation   ${r.isolation} (${r.isolation === "vm" ? "tart" : "docker"})\n` +
        `  image       ${r.vm.image}\n` +
        `  refresh     ${r.vm.refresh}\n` +
        `  daemon      ${daemonUp ? pc.green("running") : pc.red("not running")}` +
        (envUp === undefined ? "" : `   env ${envUp ? pc.green("up") : pc.yellow("down")}`) +
        (state ? `\n  created     ${state.createdAt} · jobs run: ${state.jobsRun}` : "")
    );
  }
  p.outro("");
}

async function refresh(repo) {
  if (!repo) {
    console.error("Usage: lw vm refresh owner/repo");
    process.exitCode = 1;
    return;
  }
  const entry = findRunner(loadConfig(), repo);
  if (!entry?.vm) {
    console.error(`${repo} has no isolated runner.`);
    process.exitCode = 1;
    return;
  }
  const name = entry.isolation === "container" ? containerName(repo) : vmName(repo);
  const statePath = path.join(ROOT, "vm", `${name}.json`);
  let state = readVmState(name) || { createdAt: timestamp(), jobsRun: 0 };
  state.recreateRequested = true;
  fs.mkdirSync(path.dirname(statePath), { recursive: true });
  fs.writeFileSync(statePath, JSON.stringify(state, null, 2) + "\n");
  svc.kickstart(runnerLabel(repo));
  console.log(`${name} marked for rebuild — the daemon is recreating it from ${entry.vm.image}.`);
}
