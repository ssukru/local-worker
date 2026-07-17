import fs from "node:fs";
import path from "node:path";
import {
  RUNNERS_DIR,
  ROOT,
  sh,
  trySh,
  ghJson,
  repoId,
  runnerLabel,
  agentPlistPath,
  loadAgent,
  unloadAgent,
  machineArch,
  ensureDirs,
} from "./util.js";
import { runnerPlist } from "./plist.js";

/** Resolve the download URL for the latest actions/runner build for this Mac. */
export function latestRunnerAsset() {
  const arch = machineArch();
  const release = ghJson(["repos/actions/runner/releases/latest"]);
  const wanted = `osx-${arch}`;
  const asset = release.assets.find(
    (a) =>
      a.name.startsWith(`actions-runner-${wanted}-`) &&
      a.name.endsWith(".tar.gz") &&
      !a.name.includes("noexternals") &&
      !a.name.includes("noruntime")
  );
  if (!asset) throw new Error(`No runner asset found for ${wanted}`);
  return { version: release.tag_name, url: asset.browser_download_url, name: asset.name };
}

/**
 * Download and extract the runner distribution once per version, then copy it
 * into per-repo instance directories. Copies (not symlinks) because each
 * instance stores its own credentials and work directory.
 */
export function downloadRunnerDist(asset) {
  const cacheDir = path.join(ROOT, "dist", asset.version);
  const marker = path.join(cacheDir, ".complete");
  if (fs.existsSync(marker)) return cacheDir;
  fs.rmSync(cacheDir, { recursive: true, force: true });
  fs.mkdirSync(cacheDir, { recursive: true });
  const tarball = path.join(cacheDir, asset.name);
  sh("curl", ["-fsSL", "-o", tarball, asset.url]);
  sh("tar", ["xzf", tarball, "-C", cacheDir]);
  fs.rmSync(tarball);
  fs.writeFileSync(marker, "");
  return cacheDir;
}

export function runnerDir(repo) {
  return path.join(RUNNERS_DIR, repoId(repo));
}

export function registrationToken(repo) {
  return sh("gh", [
    "api", "-X", "POST",
    `repos/${repo}/actions/runners/registration-token`,
    "--jq", ".token",
  ]);
}

export function removalToken(repo) {
  return sh("gh", [
    "api", "-X", "POST",
    `repos/${repo}/actions/runners/remove-token`,
    "--jq", ".token",
  ]);
}

/** Configure a runner instance for a repo (idempotent via --replace). */
export function configureRunner({ repo, name, labels, distDir }) {
  ensureDirs();
  const dir = runnerDir(repo);
  if (!fs.existsSync(path.join(dir, "config.sh"))) {
    fs.rmSync(dir, { recursive: true, force: true });
    fs.mkdirSync(dir, { recursive: true });
    fs.cpSync(distDir, dir, { recursive: true });
    fs.rmSync(path.join(dir, ".complete"), { force: true });
  }
  const token = registrationToken(repo);
  sh("./config.sh", [
    "--url", `https://github.com/${repo}`,
    "--token", token,
    "--name", name,
    "--labels", labels.join(","),
    "--work", "_work",
    "--unattended",
    "--replace",
  ], { cwd: dir, stdio: ["ignore", "pipe", "pipe"] });
  return dir;
}

export function installRunnerService(repo) {
  const label = runnerLabel(repo);
  const plistPath = agentPlistPath(label);
  unloadAgent(label, plistPath);
  fs.writeFileSync(plistPath, runnerPlist(label, runnerDir(repo)));
  loadAgent(plistPath);
  return label;
}

export function uninstallRunnerService(repo) {
  const label = runnerLabel(repo);
  const plistPath = agentPlistPath(label);
  unloadAgent(label, plistPath);
  fs.rmSync(plistPath, { force: true });
}

/** Deregister from GitHub and delete the local instance directory. */
export function deregisterRunner(repo) {
  const dir = runnerDir(repo);
  if (fs.existsSync(path.join(dir, "config.sh"))) {
    const token = trySh("gh", [
      "api", "-X", "POST",
      `repos/${repo}/actions/runners/remove-token`,
      "--jq", ".token",
    ]);
    if (token) {
      try {
        sh("./config.sh", ["remove", "--token", token], {
          cwd: dir,
          stdio: ["ignore", "pipe", "pipe"],
        });
      } catch {
        // Repo may be gone or token rejected; still clean up locally.
      }
    }
  }
  fs.rmSync(dir, { recursive: true, force: true });
}

/** Remote view: what GitHub thinks of the runners registered to a repo. */
export function remoteRunners(repo) {
  try {
    const data = ghJson([`repos/${repo}/actions/runners`]);
    return data.runners ?? [];
  } catch {
    return null; // no access / network down
  }
}

/** Local view: is the Runner.Listener process for this repo alive? */
export function listenerRunning(repo) {
  const out = trySh("pgrep", ["-fl", "Runner.Listener"]);
  if (!out) return false;
  return out.split("\n").some((line) => line.includes(runnerDir(repo)));
}
