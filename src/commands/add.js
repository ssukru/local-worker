import os from "node:os";
import * as p from "@clack/prompts";
import pc from "picocolors";
import { loadConfig, saveConfig, findRunner } from "../config.js";
import {
  latestRunnerAsset,
  downloadRunnerDist,
  configureRunner,
  installRunnerService,
  remoteRunners,
} from "../runner.js";
import { sh, ghAuthed, ghLogin, machineArch, isTTY, timestamp, repoId } from "../util.js";

const DEFAULT_LABELS = ["local-worker"];

function machineName() {
  return os.hostname().replace(/\.local$/, "").toLowerCase();
}

export async function pickRepo() {
  const s = p.spinner();
  s.start("Fetching your repositories");
  const repos = JSON.parse(
    sh("gh", [
      "repo", "list",
      "--limit", "100",
      "--no-archived",
      "--json", "nameWithOwner,isPrivate,pushedAt",
    ])
  ).sort((a, b) => (a.pushedAt < b.pushedAt ? 1 : -1));
  s.stop(`Found ${repos.length} repositories`);

  const choice = await p.select({
    message: "Which repository should send jobs to this Mac?",
    options: [
      ...repos.slice(0, 30).map((r) => ({
        value: r.nameWithOwner,
        label: r.nameWithOwner,
        hint: r.isPrivate ? "private" : pc.yellow("PUBLIC — read the security note"),
      })),
      { value: "__other", label: "Type an owner/repo manually…" },
    ],
  });
  if (p.isCancel(choice)) return null;
  if (choice !== "__other") return choice;

  const typed = await p.text({
    message: "Repository (owner/repo):",
    validate: (v) => (/^[\w.-]+\/[\w.-]+$/.test(v) ? undefined : "Expected owner/repo"),
  });
  return p.isCancel(typed) ? null : typed;
}

export async function add(repoArg, flags = {}, { quietIntro = false } = {}) {
  if (!quietIntro) p.intro(pc.bgCyan(pc.black(" local-worker · add runner ")));

  if (!ghAuthed()) {
    p.log.error("GitHub CLI is not authenticated. Run `gh auth login` first (or `lw setup`).");
    process.exitCode = 1;
    return;
  }

  let repo = repoArg;
  if (!repo) {
    if (!isTTY()) {
      p.log.error("No repo given. Usage: lw add owner/repo");
      process.exitCode = 1;
      return;
    }
    repo = await pickRepo();
    if (!repo) return p.cancel("Cancelled.");
  }

  const config = loadConfig();
  if (findRunner(config, repo)) {
    p.log.warn(`${repo} is already attached. Re-registering it (safe, uses --replace).`);
  }

  // Verify access + visibility before doing anything.
  const info = JSON.parse(
    sh("gh", ["api", `repos/${repo}`, "--jq", '{private: .private, admin: .permissions.admin}'])
  );
  if (!info.admin) {
    p.log.error(`You need admin access on ${repo} to register runners.`);
    process.exitCode = 1;
    return;
  }
  if (!info.private) {
    p.log.warn(
      `${repo} is ${pc.yellow("PUBLIC")}. Self-hosted runners on public repos can be attacked via fork PRs.\n` +
        `If you continue: in repo Settings → Actions, restrict which workflows can run and\n` +
        `never allow pull_request workflows from forks to reach this runner.`
    );
    if (isTTY() && !flags.yes) {
      const ok = await p.confirm({ message: "Continue anyway?", initialValue: false });
      if (p.isCancel(ok) || !ok) return p.cancel("Cancelled.");
    } else if (!flags.yes) {
      p.log.error("Refusing to attach a public repo non-interactively without --yes.");
      process.exitCode = 1;
      return;
    }
  }

  const extraLabels = flags.labels
    ? String(flags.labels).split(",").map((l) => l.trim()).filter(Boolean)
    : DEFAULT_LABELS;
  const name = flags.name || `${machineName()}-${repoId(repo).slice(0, 40)}`;

  const s = p.spinner();
  s.start("Downloading the GitHub Actions runner");
  const asset = latestRunnerAsset();
  const distDir = downloadRunnerDist(asset);
  s.stop(`Runner ${asset.version} ready (${machineArch()})`);

  s.start(`Registering "${name}" with ${repo}`);
  configureRunner({ repo, name, labels: extraLabels, distDir });
  s.stop(`Registered with labels: self-hosted, macOS, ${machineArch() === "arm64" ? "ARM64" : "X64"}, ${extraLabels.join(", ")}`);

  s.start("Installing launchd service (starts at login, restarts on crash)");
  const label = installRunnerService(repo);
  s.stop(`Service loaded: ${label}`);

  const entry = {
    repo,
    name,
    labels: extraLabels,
    addedAt: timestamp(),
    addedBy: ghLogin(),
  };
  config.runners = config.runners.filter(
    (r) => r.repo.toLowerCase() !== repo.toLowerCase()
  );
  config.runners.push(entry);
  saveConfig(config);

  // Confirm GitHub sees it online.
  s.start("Waiting for GitHub to see the runner online");
  let online = false;
  for (let i = 0; i < 10 && !online; i++) {
    await new Promise((r) => setTimeout(r, 2000));
    const remote = remoteRunners(repo) || [];
    online = remote.some((r) => r.name === name && r.status === "online");
  }
  s.stop(online ? pc.green("Runner is ONLINE on GitHub ✔") : pc.yellow("Not online yet — check `lw status` in a minute"));

  p.note(
    `Route jobs here by changing workflows in ${repo}:\n` +
      `  runs-on: [self-hosted, macOS]\n\n` +
      `Run ${pc.cyan(`lw integrate ${repo}`)} for a copy-paste patch with a\n` +
      `hosted-runner fallback input.`,
    "Next step"
  );
  if (!quietIntro) p.outro("Done.");
}
