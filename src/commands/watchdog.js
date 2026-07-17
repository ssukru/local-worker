import path from "node:path";
import { fileURLToPath } from "node:url";
import * as p from "@clack/prompts";
import pc from "picocolors";
import { loadConfig, saveConfig } from "../config.js";
import { listenerRunning, remoteRunners } from "../runner.js";
import { WATCHDOG_LABEL, LOGS_DIR, runnerLabel, trySh, timestamp, isTTY } from "../util.js";
import { svc } from "../service/index.js";

const CLI_ENTRY = fileURLToPath(new URL("../../bin/lw.js", import.meta.url));

export async function watchdog(sub, flags = {}) {
  switch (sub) {
    case "install": return install(flags);
    case "uninstall": return uninstall();
    case "check": return check();
    case "status":
    default:
      console.log(
        svc.isLoaded(WATCHDOG_LABEL)
          ? "watchdog: active"
          : "watchdog: not running (lw watchdog install)"
      );
  }
}

async function install(flags) {
  p.intro(pc.bgCyan(pc.black(" local-worker · watchdog ")));
  const config = loadConfig();

  let healthchecksUrl = flags.healthchecks ?? config.watchdog.healthchecksUrl;
  if (isTTY() && flags.healthchecks === undefined) {
    p.log.info(
      "The watchdog restarts dead runners and pings a dead-man's-switch URL.\n" +
        "If this machine goes fully offline, the missing pings trigger the alert —\n" +
        "create a free check at https://healthchecks.io and paste its ping URL."
    );
    const url = await p.text({
      message: "Healthchecks.io ping URL (Enter to skip):",
      defaultValue: healthchecksUrl,
      placeholder: "https://hc-ping.com/…",
    });
    if (p.isCancel(url)) return p.cancel("Cancelled.");
    healthchecksUrl = (url || "").trim();
  }

  const interval = parseInt(flags.interval ?? config.watchdog.intervalSeconds, 10) || 300;
  config.watchdog = { ...config.watchdog, installed: true, intervalSeconds: interval, healthchecksUrl };
  saveConfig(config);

  svc.uninstall(WATCHDOG_LABEL);
  svc.install({
    label: WATCHDOG_LABEL,
    programArgs: [process.execPath, CLI_ENTRY, "watchdog", "check"],
    logPath: path.join(LOGS_DIR, `${WATCHDOG_LABEL}.log`),
    startInterval: interval,
  });
  p.outro(`Watchdog active — every ${interval}s${healthchecksUrl ? " + offline alerts via healthchecks.io" : ""}.`);
}

async function uninstall() {
  const config = loadConfig();
  svc.uninstall(WATCHDOG_LABEL);
  config.watchdog.installed = false;
  saveConfig(config);
  console.log("Watchdog removed.");
}

/** One health pass. Runs headless from the service manager — plain output only. */
async function check() {
  const config = loadConfig();
  const report = [];

  for (const r of config.runners) {
    if (listenerRunning(r.repo)) {
      report.push(`${r.repo}: listener ok`);
      continue;
    }
    // Listener is dead: kick the service (its own restart policy usually beats us).
    const kicked = svc.kickstart(runnerLabel(r.repo));
    report.push(`${r.repo}: listener DOWN — kickstart ${kicked ? "ok" : "FAILED"}`);
    await notify(config, `local-worker: runner for ${r.repo} was down — restart ${kicked ? "succeeded" : "FAILED"}`);
  }

  // GitHub's view (skippable if offline — that's what the dead-man ping covers).
  for (const r of config.runners) {
    const remote = remoteRunners(r.repo);
    const mine = remote?.find((x) => x.name === r.name);
    if (remote && mine && mine.status !== "online") {
      report.push(`${r.repo}: GitHub reports ${mine.status}`);
    }
  }

  if (config.watchdog.healthchecksUrl) {
    const ping = trySh("curl", ["-fsS", "-m", "10", "-o", "/dev/null", config.watchdog.healthchecksUrl]);
    report.push(ping === null ? "dead-man ping FAILED" : "dead-man ping sent");
  }

  console.log(`[${timestamp()}] ${report.join(" | ")}`);
}

async function notify(config, text) {
  const { botToken, chatId } = config.watchdog.telegram || {};
  if (!botToken || !chatId) return;
  trySh("curl", [
    "-fsS", "-m", "10", "-o", "/dev/null",
    `https://api.telegram.org/bot${botToken}/sendMessage`,
    "-d", `chat_id=${chatId}`,
    "--data-urlencode", `text=${text}`,
  ]);
}
