// macOS service backend: LaunchAgents in the user's GUI session.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const AGENTS_DIR = path.join(os.homedir(), "Library", "LaunchAgents");
const uid = () => process.getuid();
const launchctl = (...args) => spawnSync("launchctl", args, { encoding: "utf8" });
const plistPath = (label) => path.join(AGENTS_DIR, `${label}.plist`);

const esc = (s) =>
  s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

export function buildPlist({ label, programArgs, workingDir, logPath, keepAlive, startInterval, env = {} }) {
  const envEntries = Object.entries(env)
    .map(([k, v]) => `      <key>${esc(k)}</key>\n      <string>${esc(v)}</string>`)
    .join("\n");
  const args = programArgs.map((a) => `      <string>${esc(a)}</string>`).join("\n");
  const extras = [];
  if (keepAlive) {
    extras.push("    <key>KeepAlive</key>", "    <true/>");
    // GUI session + Interactive keep Xcode/simulator builds working.
    extras.push("    <key>ProcessType</key>", "    <string>Interactive</string>");
    extras.push("    <key>SessionCreate</key>", "    <true/>");
  }
  if (startInterval) {
    extras.push("    <key>StartInterval</key>", `    <integer>${startInterval}</integer>`);
  }
  if (workingDir) {
    extras.push("    <key>WorkingDirectory</key>", `    <string>${esc(workingDir)}</string>`);
  }
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>${esc(label)}</string>
    <key>ProgramArguments</key>
    <array>
${args}
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${esc(logPath)}</string>
    <key>StandardErrorPath</key>
    <string>${esc(logPath)}</string>
    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key>
      <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
      <key>LANG</key>
      <string>en_US.UTF-8</string>
${envEntries}
    </dict>
${extras.join("\n")}
  </dict>
</plist>
`;
}

export function isLoaded(label) {
  return launchctl("print", `gui/${uid()}/${label}`).status === 0;
}

export function install(spec) {
  fs.mkdirSync(AGENTS_DIR, { recursive: true });
  const file = plistPath(spec.label);
  unload(spec.label, file);
  fs.writeFileSync(file, buildPlist(spec));
  fs.chmodSync(file, 0o600); // may carry GH_TOKEN in EnvironmentVariables
  const res = launchctl("bootstrap", `gui/${uid()}`, file);
  if (res.status !== 0) {
    const legacy = launchctl("load", "-w", file);
    if (legacy.status !== 0) {
      throw new Error(`launchctl failed to load ${file}:\n${res.stderr}${legacy.stderr}`);
    }
  }
}

function unload(label, file) {
  if (isLoaded(label)) {
    const res = launchctl("bootout", `gui/${uid()}/${label}`);
    if (res.status !== 0) launchctl("unload", "-w", file);
  }
}

export function uninstall(label) {
  const file = plistPath(label);
  unload(label, file);
  fs.rmSync(file, { force: true });
}

export function kickstart(label) {
  return launchctl("kickstart", "-k", `gui/${uid()}/${label}`).status === 0;
}
