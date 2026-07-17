// Linux service backend: systemd user units.
//
// Runners get a plain service with Restart=always; the watchdog gets a
// oneshot service driven by a timer. User units stop at logout unless
// lingering is enabled — `lw doctor` checks for that.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const UNIT_DIR = path.join(os.homedir(), ".config", "systemd", "user");
const systemctl = (...args) =>
  spawnSync("systemctl", ["--user", ...args], { encoding: "utf8" });

const quote = (a) => `"${a.replaceAll('"', '\\"')}"`;

export function buildService({ label, programArgs, workingDir, logPath, keepAlive, env = {} }) {
  const envLines = Object.entries(env)
    .map(([k, v]) => `Environment=${quote(`${k}=${v}`)}\n`)
    .join("");
  return `[Unit]
Description=local-worker ${label}
After=network-online.target

[Service]
${keepAlive ? "Restart=always\nRestartSec=5\n" : "Type=oneshot\n"}ExecStart=${programArgs.map(quote).join(" ")}
${workingDir ? `WorkingDirectory=${workingDir}\n` : ""}StandardOutput=append:${logPath}
StandardError=append:${logPath}
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/home/linuxbrew/.linuxbrew/bin
${envLines}
[Install]
WantedBy=default.target
`;
}

export function buildTimer(label, intervalSeconds) {
  return `[Unit]
Description=local-worker timer for ${label}

[Timer]
OnBootSec=60
OnUnitActiveSec=${intervalSeconds}

[Install]
WantedBy=timers.target
`;
}

function unitPath(name) {
  return path.join(UNIT_DIR, name);
}

export function install(spec) {
  fs.mkdirSync(UNIT_DIR, { recursive: true });
  const serviceName = `${spec.label}.service`;
  fs.writeFileSync(
    unitPath(serviceName),
    buildService({ ...spec, keepAlive: spec.keepAlive && !spec.startInterval })
  );
  fs.chmodSync(unitPath(serviceName), 0o600); // may carry GH_TOKEN in Environment=
  let target = serviceName;
  if (spec.startInterval) {
    target = `${spec.label}.timer`;
    fs.writeFileSync(unitPath(target), buildTimer(spec.label, spec.startInterval));
  }
  systemctl("daemon-reload");
  const res = systemctl("enable", "--now", target);
  if (res.status !== 0) {
    throw new Error(
      `systemctl --user enable --now ${target} failed:\n${res.stderr}\n` +
        `Is a systemd user session available? (On servers/SSH: loginctl enable-linger ${os.userInfo().username})`
    );
  }
}

export function isLoaded(label) {
  return (
    systemctl("is-enabled", `${label}.service`).status === 0 ||
    systemctl("is-enabled", `${label}.timer`).status === 0
  );
}

export function uninstall(label) {
  for (const name of [`${label}.timer`, `${label}.service`]) {
    systemctl("disable", "--now", name);
    fs.rmSync(unitPath(name), { force: true });
  }
  systemctl("daemon-reload");
}

export function kickstart(label) {
  return systemctl("restart", `${label}.service`).status === 0;
}
