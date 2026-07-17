import path from "node:path";
import { LOGS_DIR } from "./util.js";

const esc = (s) =>
  s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

function plist(label, programArgs, extra = "") {
  const args = programArgs
    .map((a) => `      <string>${esc(a)}</string>`)
    .join("\n");
  const log = path.join(LOGS_DIR, `${label}.log`);
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
    <key>StandardOutPath</key>
    <string>${esc(log)}</string>
    <key>StandardErrorPath</key>
    <string>${esc(log)}</string>
    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key>
      <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
      <key>LANG</key>
      <string>en_US.UTF-8</string>
    </dict>
${extra}  </dict>
</plist>
`;
}

/** LaunchAgent for a runner: starts at login, restarts if it dies. */
export function runnerPlist(label, runnerDir) {
  return plist(label, [path.join(runnerDir, "run.sh")], `    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>WorkingDirectory</key>
    <string>${runnerDir}</string>
    <key>ProcessType</key>
    <string>Interactive</string>
    <key>SessionCreate</key>
    <true/>
`);
}

/** LaunchAgent for the watchdog: periodic health check. */
export function watchdogPlist(label, nodeBin, cliEntry, intervalSeconds) {
  return plist(label, [nodeBin, cliEntry, "watchdog", "check"], `    <key>RunAtLoad</key>
    <true/>
    <key>StartInterval</key>
    <integer>${intervalSeconds}</integer>
`);
}
