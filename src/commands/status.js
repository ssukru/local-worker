import * as p from "@clack/prompts";
import pc from "picocolors";
import { loadConfig } from "../config.js";
import { remoteRunners, listenerRunning } from "../runner.js";
import { runnerLabel, WATCHDOG_LABEL, osLabel } from "../util.js";
import { svc } from "../service/index.js";

export async function status() {
  const config = loadConfig();
  p.intro(pc.bgCyan(pc.black(" local-worker · status ")));

  if (!config.runners.length) {
    p.log.info("No runners attached yet. Run `lw setup` or `lw add owner/repo`.");
    p.outro("");
    return;
  }

  for (const r of config.runners) {
    const service = svc.isLoaded(runnerLabel(r.repo));
    const listening = listenerRunning(r.repo);
    const remote = remoteRunners(r.repo);
    const mine = remote?.find((x) => x.name === r.name);

    const github = !remote
      ? pc.yellow("unknown (no network/access)")
      : !mine
        ? pc.red("NOT REGISTERED")
        : mine.status === "online"
          ? mine.busy
            ? pc.cyan("online · running a job")
            : pc.green("online · idle")
          : pc.red("offline");

    p.log.message(
      `${pc.bold(r.repo)}\n` +
        `  runner name   ${r.name}\n` +
        `  labels        self-hosted, ${osLabel()} + ${r.labels.join(", ")}\n` +
        `  service       ${service ? pc.green("loaded") : pc.red("not loaded")}   ` +
        `listener ${listening ? pc.green("running") : pc.red("stopped")}\n` +
        `  github sees   ${github}`
    );
  }

  p.log.message(
    `${pc.bold("watchdog")}      ${
      config.watchdog.installed
        ? svc.isLoaded(WATCHDOG_LABEL)
          ? pc.green(`active (every ${config.watchdog.intervalSeconds}s)`)
          : pc.red("installed but not loaded")
        : pc.dim("not installed")
    }`
  );
  p.outro("");
}
