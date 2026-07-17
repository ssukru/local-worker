import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { LOGS_DIR, runnerLabel, WATCHDOG_LABEL } from "../util.js";
import { loadConfig } from "../config.js";

export async function logs(target) {
  const config = loadConfig();
  let label;
  if (!target || target === "watchdog") {
    label = target === "watchdog" || !config.runners.length
      ? WATCHDOG_LABEL
      : runnerLabel(config.runners[0].repo);
  } else {
    label = runnerLabel(target);
  }
  const file = path.join(LOGS_DIR, `${label}.log`);
  if (!fs.existsSync(file)) {
    console.error(`No log file yet: ${file}`);
    process.exitCode = 1;
    return;
  }
  spawnSync("tail", ["-n", "100", "-f", file], { stdio: "inherit" });
}
