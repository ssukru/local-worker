import fs from "node:fs";
import { CONFIG_PATH, ensureDirs } from "./util.js";

const DEFAULTS = {
  runners: [],
  watchdog: {
    installed: false,
    intervalSeconds: 300,
    healthchecksUrl: "",
    telegram: { botToken: "", chatId: "" },
  },
};

export function loadConfig() {
  try {
    const raw = JSON.parse(fs.readFileSync(CONFIG_PATH, "utf8"));
    return {
      ...DEFAULTS,
      ...raw,
      watchdog: { ...DEFAULTS.watchdog, ...raw.watchdog },
    };
  } catch {
    return structuredClone(DEFAULTS);
  }
}

export function saveConfig(config) {
  ensureDirs();
  fs.writeFileSync(CONFIG_PATH, JSON.stringify(config, null, 2) + "\n", {
    mode: 0o600,
  });
}

export function findRunner(config, repo) {
  return config.runners.find(
    (r) => r.repo.toLowerCase() === repo.toLowerCase()
  );
}
