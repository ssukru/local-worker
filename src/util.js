import { execFileSync, spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import fs from "node:fs";

export const HOME = os.homedir();
export const ROOT = path.join(HOME, ".local-worker");
export const RUNNERS_DIR = path.join(ROOT, "runners");
export const LOGS_DIR = path.join(ROOT, "logs");
export const CONFIG_PATH = path.join(ROOT, "config.json");
export const AGENTS_DIR = path.join(HOME, "Library", "LaunchAgents");

export function ensureDirs() {
  for (const dir of [ROOT, RUNNERS_DIR, LOGS_DIR, AGENTS_DIR]) {
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
  const out = trySh("gh", ["api", "user", "--jq", ".login"]);
  return out || null;
}

/** "owner/repo" -> filesystem/launchd-safe id like "owner__repo". */
export function repoId(repo) {
  return repo.replace(/[^a-zA-Z0-9-]/g, "__");
}

export function runnerLabel(repo) {
  return `com.local-worker.runner.${repoId(repo)}`;
}

export const WATCHDOG_LABEL = "com.local-worker.watchdog";

export function agentPlistPath(label) {
  return path.join(AGENTS_DIR, `${label}.plist`);
}

export function uid() {
  return process.getuid();
}

export function launchctl(...args) {
  return spawnSync("launchctl", args, { encoding: "utf8" });
}

/** Is a launchd job loaded in the current GUI session? */
export function agentLoaded(label) {
  return launchctl("print", `gui/${uid()}/${label}`).status === 0;
}

export function loadAgent(plistPath) {
  // bootstrap is the modern API; `load -w` is the fallback for older macOS.
  const res = launchctl("bootstrap", `gui/${uid()}`, plistPath);
  if (res.status !== 0) {
    const legacy = launchctl("load", "-w", plistPath);
    if (legacy.status !== 0) {
      throw new Error(
        `launchctl failed to load ${plistPath}:\n${res.stderr}${legacy.stderr}`
      );
    }
  }
}

export function unloadAgent(label, plistPath) {
  if (agentLoaded(label)) {
    const res = launchctl("bootout", `gui/${uid()}/${label}`);
    if (res.status !== 0) launchctl("unload", "-w", plistPath);
  }
}

export function kickstartAgent(label) {
  return launchctl("kickstart", "-k", `gui/${uid()}/${label}`).status === 0;
}

export function machineArch() {
  // Report arm64 even when Node itself runs under Rosetta.
  if (process.arch === "arm64") return "arm64";
  return trySh("sysctl", ["-n", "hw.optional.arm64"]) === "1" ? "arm64" : "x64";
}

export function isTTY() {
  return process.stdout.isTTY && process.stdin.isTTY;
}

export function timestamp() {
  return new Date().toISOString();
}
