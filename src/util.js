import { execFileSync, spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import fs from "node:fs";

export const HOME = os.homedir();
export const ROOT = path.join(HOME, ".local-worker");
export const RUNNERS_DIR = path.join(ROOT, "runners");
export const LOGS_DIR = path.join(ROOT, "logs");
export const CONFIG_PATH = path.join(ROOT, "config.json");

export function ensureDirs() {
  for (const dir of [ROOT, RUNNERS_DIR, LOGS_DIR]) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

/** Run a command, capture stdout, throw on failure. */
export function sh(cmd, args = [], opts = {}) {
  return execFileSync(cmd, args, { encoding: "utf8", ...opts }).trim();
}

/** Run a command, capture stdout, return null on failure instead of throwing. */
export function trySh(cmd, args = [], opts = {}) {
  try {
    return sh(cmd, args, { stdio: ["ignore", "pipe", "ignore"], ...opts });
  } catch {
    return null;
  }
}

/** Run a command with inherited stdio (interactive). Throws on non-zero exit. */
export function run(cmd, args = [], opts = {}) {
  const res = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (res.error) throw res.error;
  if (res.status !== 0) {
    throw new Error(`${cmd} ${args.join(" ")} exited with code ${res.status}`);
  }
}

/** gh api helper returning parsed JSON. */
export function ghJson(args) {
  const out = sh("gh", ["api", ...args]);
  return out ? JSON.parse(out) : null;
}

export function ghAuthed() {
  return spawnSync("gh", ["auth", "status"], { stdio: "ignore" }).status === 0;
}

export function ghLogin() {
  return trySh("gh", ["api", "user", "--jq", ".login"]);
}

/** "owner/repo" -> filesystem/service-safe id like "owner__repo". */
export function repoId(repo) {
  return repo.replace(/[^a-zA-Z0-9-]/g, "__");
}

export function runnerLabel(repo) {
  return `com.local-worker.runner.${repoId(repo)}`;
}

export const WATCHDOG_LABEL = "com.local-worker.watchdog";

export function machineArch() {
  if (process.platform === "darwin" && process.arch !== "arm64") {
    // Node itself may be running under Rosetta on an Apple Silicon Mac.
    return trySh("sysctl", ["-n", "hw.optional.arm64"]) === "1" ? "arm64" : "x64";
  }
  return process.arch === "arm64" ? "arm64" : "x64";
}

/** OS slug as used in actions/runner release asset names. */
export function runnerOsSlug() {
  return process.platform === "darwin" ? "osx" : "linux";
}

/** Human label GitHub auto-assigns to runners on this OS. */
export function osLabel() {
  return process.platform === "darwin" ? "macOS" : "Linux";
}

export function isTTY() {
  return process.stdout.isTTY && process.stdin.isTTY;
}

export function timestamp() {
  return new Date().toISOString();
}
