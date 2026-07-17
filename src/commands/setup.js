import * as p from "@clack/prompts";
import pc from "picocolors";
import { run, trySh, ghAuthed, ghLogin, isTTY } from "../util.js";
import { collectChecks } from "./doctor.js";
import { add, pickRepo } from "./add.js";
import { watchdog } from "./watchdog.js";
import { integrate } from "./integrate.js";

export async function setup(flags = {}) {
  p.intro(pc.bgCyan(pc.black(" local-worker · setup ")));
  p.log.message(
    `This wizard turns this Mac into a self-hosted GitHub Actions runner:\n` +
      `  1. check tools & machine settings\n` +
      `  2. sign in to GitHub\n` +
      `  3. attach one or more repositories\n` +
      `  4. install the health watchdog\n` +
      `Everything lives in ${pc.cyan("~/.local-worker")}; run it again anytime.`
  );

  if (!isTTY()) {
    p.log.error("setup is interactive — run it in a terminal. For scripting, use `lw add owner/repo --yes`.");
    process.exitCode = 1;
    return;
  }

  // 1. GitHub CLI presence + auth ------------------------------------------
  if (!trySh("gh", ["--version"])) {
    const doInstall = await p.confirm({
      message: "GitHub CLI (gh) is required and not installed. Install it with Homebrew now?",
    });
    if (p.isCancel(doInstall) || !doInstall) return p.cancel("Cannot continue without gh.");
    run("brew", ["install", "gh"]);
  }
  if (!ghAuthed()) {
    p.log.step("Sign in to GitHub — a browser window will guide you (device flow).");
    run("gh", ["auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web"]);
  }
  p.log.success(`Signed in as ${pc.bold(ghLogin())}`);

  // 2. Machine readiness ----------------------------------------------------
  p.log.step("Machine check");
  const checks = collectChecks({ forSetup: true });
  for (const c of checks) {
    if (c.level === "ok") p.log.success(c.msg);
    else if (c.level === "warn") p.log.warn(c.msg);
    else p.log.error(c.msg);
  }
  const powerIssues = checks.some(
    (c) => c.level !== "ok" && /sleep|Auto-restart|Linger|suspend/i.test(c.msg)
  );
  if (powerIssues && process.platform === "darwin") {
    const fix = await p.confirm({
      message: "Apply recommended power settings now? (runs sudo pmset — you'll be asked for your password)",
    });
    if (!p.isCancel(fix) && fix) {
      run("sudo", ["pmset", "-a", "sleep", "0", "disksleep", "0", "displaysleep", "10", "autorestart", "1", "womp", "1"]);
      run("sudo", ["pmset", "-a", "disablesleep", "1"]);
      p.log.success("Power settings applied — this Mac will stay awake, even with the lid closed.");
    }
  } else if (powerIssues && process.platform === "linux") {
    const fix = await p.confirm({
      message: "Enable lingering + mask suspend targets now? (uses loginctl and sudo systemctl)",
    });
    if (!p.isCancel(fix) && fix) {
      run("loginctl", ["enable-linger", process.env.USER]);
      run("sudo", ["systemctl", "mask", "sleep.target", "suspend.target", "hibernate.target", "hybrid-sleep.target"]);
      p.log.success("This machine will stay available for jobs.");
    }
  }
  if (checks.some((c) => c.level === "bad")) {
    const cont = await p.confirm({
      message: "There are blocking issues above. Continue anyway?",
      initialValue: false,
    });
    if (p.isCancel(cont) || !cont) return p.cancel("Fix the issues and re-run `lw setup`.");
  }

  // 3. Attach repositories --------------------------------------------------
  let attachMore = true;
  let attached = 0;
  while (attachMore) {
    const repo = await pickRepo();
    if (!repo) break;
    await add(repo, flags, { quietIntro: true });
    attached++;
    const again = await p.confirm({ message: "Attach another repository?", initialValue: false });
    attachMore = !p.isCancel(again) && again;
  }

  // 4. Watchdog -------------------------------------------------------------
  const wantWatchdog = await p.confirm({
    message: "Install the watchdog? (restarts dead runners, alerts you if this Mac goes offline)",
    initialValue: true,
  });
  if (!p.isCancel(wantWatchdog) && wantWatchdog) {
    await watchdog("install", flags);
  }

  // Done --------------------------------------------------------------------
  if (attached) {
    p.log.step("How to route jobs to this Mac:");
    await integrate();
  }
  p.outro(
    pc.green("Setup complete. ") +
      `Useful commands: ${pc.cyan("lw status")}, ${pc.cyan("lw doctor")}, ${pc.cyan("lw logs")}.`
  );
}
