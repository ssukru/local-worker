import * as p from "@clack/prompts";
import pc from "picocolors";
import { loadConfig, saveConfig, findRunner } from "../config.js";
import { deregisterRunner, uninstallRunnerService } from "../runner.js";
import { isTTY } from "../util.js";

export async function remove(repo, flags = {}) {
  p.intro(pc.bgCyan(pc.black(" local-worker · remove runner ")));
  const config = loadConfig();

  if (!repo) {
    if (!isTTY() || !config.runners.length) {
      p.log.error("Usage: lw remove owner/repo");
      process.exitCode = 1;
      return;
    }
    const choice = await p.select({
      message: "Which runner should be removed?",
      options: config.runners.map((r) => ({ value: r.repo, label: r.repo })),
    });
    if (p.isCancel(choice)) return p.cancel("Cancelled.");
    repo = choice;
  }

  const entry = findRunner(config, repo);
  if (!entry) {
    p.log.warn(`${repo} is not in the local config — cleaning up anyway.`);
  }

  if (isTTY() && !flags.yes) {
    const ok = await p.confirm({
      message: `Deregister this Mac from ${repo} and delete its work directory?`,
    });
    if (p.isCancel(ok) || !ok) return p.cancel("Cancelled.");
  }

  const s = p.spinner();
  s.start("Stopping service");
  uninstallRunnerService(repo);
  s.stop("Service removed");

  s.start("Deregistering from GitHub and deleting local files");
  deregisterRunner(repo);
  s.stop("Deregistered");

  config.runners = config.runners.filter(
    (r) => r.repo.toLowerCase() !== repo.toLowerCase()
  );
  saveConfig(config);
  p.outro(`${repo} detached.`);
}
