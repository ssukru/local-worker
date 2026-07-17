import fs from "node:fs";
import * as p from "@clack/prompts";
import pc from "picocolors";
import { loadConfig } from "../config.js";
import { trySh, ghAuthed, ghLogin, machineArch, agentLoaded, WATCHDOG_LABEL, runnerLabel } from "../util.js";

const ok = (msg) => ({ level: "ok", msg });
const warn = (msg) => ({ level: "warn", msg });
const bad = (msg) => ({ level: "bad", msg });

export function collectChecks({ forSetup = false } = {}) {
  const checks = [];
  const config = loadConfig();

  // Base tools
  checks.push(trySh("git", ["--version"]) ? ok("git installed") : bad("git missing — install Xcode Command Line Tools: xcode-select --install"));
  const ghVersion = trySh("gh", ["--version"]);
  if (!ghVersion) {
    checks.push(bad("GitHub CLI missing — brew install gh"));
  } else if (!ghAuthed()) {
    checks.push(bad("GitHub CLI not authenticated — run: gh auth login"));
  } else {
    checks.push(ok(`GitHub CLI authenticated as ${ghLogin()}`));
  }

  // Machine
  const arch = machineArch();
  checks.push(ok(`Architecture: ${arch}${arch === "arm64" ? " (Apple Silicon)" : ""}`));
  const macos = trySh("sw_vers", ["-productVersion"]);
  checks.push(ok(`macOS ${macos}`));

  const freeGb = diskFreeGb();
  if (freeGb !== null) {
    checks.push(freeGb > 40 ? ok(`Disk free: ${freeGb} GB`) : warn(`Disk free: ${freeGb} GB — builds eat disk fast; keep >40 GB free`));
  }

  // Power management — the difference between a runner and a paperweight.
  const pm = trySh("pmset", ["-g"]) || "";
  const sleepLine = pm.split("\n").find((l) => /^\s*sleep\s/.test(l)) || "";
  const sleepVal = parseInt(sleepLine.trim().split(/\s+/)[1] ?? "-1", 10);
  const sleepPrevented = /sleep prevented/.test(sleepLine);
  if (sleepVal === 0 || pm.includes("SleepDisabled 1")) {
    checks.push(ok("System sleep disabled"));
  } else if (sleepPrevented) {
    checks.push(warn(`System sleep is ${sleepVal} min but currently prevented by an app — make it permanent: sudo pmset -a sleep 0`));
  } else {
    checks.push(bad(`System sleeps after ${sleepVal} min — jobs will stall. Fix: sudo pmset -a sleep 0 disksleep 0; sudo pmset -a disablesleep 1 (keeps a MacBook awake with the lid closed)`));
  }
  if (!pm.includes("autorestart          1")) {
    checks.push(warn("Auto-restart after power failure is off — fix: sudo pmset -a autorestart 1"));
  } else {
    checks.push(ok("Auto-restart after power failure enabled"));
  }

  // Auto-login: launchd agents (and GUI-needing builds) require a logged-in user.
  const autoLogin = trySh("defaults", ["read", "/Library/Preferences/com.apple.loginwindow", "autoLoginUser"]);
  checks.push(
    autoLogin
      ? ok(`Automatic login enabled (${autoLogin})`)
      : warn("Automatic login is OFF — after a reboot the runner won't start until someone logs in. Enable: System Settings → Users & Groups → Automatic login. (If FileVault is on, macOS disables auto-login; consider turning FileVault off on a dedicated CI Mac.)")
  );

  // Optional toolchains
  const docker = trySh("docker", ["--version"]);
  if (docker) {
    const daemon = trySh("docker", ["info", "--format", "ok"]);
    checks.push(daemon ? ok(`Docker CLI + daemon running (${docker.split(",")[0]})`) : warn("Docker CLI present but daemon not running — start OrbStack/Colima/Docker Desktop and set it to launch at login"));
  } else {
    checks.push(warn("Docker not installed — needed only for image builds (brew install orbstack — or colima + docker)"));
  }

  const xcodePath = trySh("xcode-select", ["-p"]);
  if (xcodePath && xcodePath.includes("Xcode.app")) {
    const ver = (trySh("xcodebuild", ["-version"]) || "").split("\n")[0];
    checks.push(ok(`${ver || "Xcode"} installed`));
  } else {
    checks.push(warn("Full Xcode not selected — needed only for iOS builds (install from App Store, then: sudo xcode-select -s /Applications/Xcode.app)"));
  }

  const node = trySh("node", ["--version"]);
  checks.push(node ? ok(`Node ${node}`) : bad("Node missing"));

  if (!forSetup) {
    // Services
    for (const r of config.runners) {
      checks.push(
        agentLoaded(runnerLabel(r.repo))
          ? ok(`Runner service loaded: ${r.repo}`)
          : bad(`Runner service NOT loaded: ${r.repo} — re-run: lw add ${r.repo}`)
      );
    }
    if (config.watchdog.installed) {
      checks.push(agentLoaded(WATCHDOG_LABEL) ? ok("Watchdog service loaded") : bad("Watchdog installed but not loaded — run: lw watchdog install"));
    } else {
      checks.push(warn("Watchdog not installed — `lw watchdog install` adds self-healing + offline alerts"));
    }
  }

  return checks;
}

function diskFreeGb() {
  const out = trySh("df", ["-g", "/System/Volumes/Data"]) || trySh("df", ["-g", "/"]);
  if (!out) return null;
  const cols = out.split("\n").at(-1).split(/\s+/);
  const avail = parseInt(cols[3], 10);
  return Number.isFinite(avail) ? avail : null;
}

export async function doctor() {
  p.intro(pc.bgCyan(pc.black(" local-worker · doctor ")));
  const checks = collectChecks();
  for (const c of checks) {
    if (c.level === "ok") p.log.success(c.msg);
    else if (c.level === "warn") p.log.warn(c.msg);
    else p.log.error(c.msg);
  }
  const bads = checks.filter((c) => c.level === "bad").length;
  const warns = checks.filter((c) => c.level === "warn").length;
  p.outro(
    bads
      ? pc.red(`${bads} blocking issue(s), ${warns} warning(s).`)
      : warns
        ? pc.yellow(`Ready with ${warns} warning(s).`)
        : pc.green("This Mac is fully ready to take jobs.")
  );
  if (bads) process.exitCode = 1;
}
