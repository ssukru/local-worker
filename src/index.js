import pc from "picocolors";
import { assertPlatform } from "./service/index.js";
import { setup } from "./commands/setup.js";
import { add } from "./commands/add.js";
import { remove } from "./commands/remove.js";
import { status } from "./commands/status.js";
import { doctor } from "./commands/doctor.js";
import { watchdog } from "./commands/watchdog.js";
import { vm } from "./commands/vm.js";
import { integrate } from "./commands/integrate.js";
import { logs } from "./commands/logs.js";

const HELP = `
${pc.bold("local-worker")} — turn this machine into a self-hosted GitHub Actions runner fleet

${pc.bold("Usage:")} lw <command> [args]

${pc.bold("Commands:")}
  setup                    Guided first-time setup (deps, auth, repos, watchdog)
  add [owner/repo]         Attach this Mac as a runner for a repo
  remove <owner/repo>      Detach a runner and clean up
  status                   Local + GitHub view of all runners
  doctor                   Diagnose machine readiness (power, tools, network)
  watchdog <install|uninstall|check|status>
                           Self-healing health checks + dead-man's-switch ping
  vm <status|refresh>      Manage isolated (VM/container) runner environments
  integrate <owner/repo>   Print the workflow changes to route jobs here
  logs [owner/repo]        Tail runner / watchdog logs

${pc.bold("Common flags:")}
  --labels a,b,c           Extra runner labels (add)
  --isolation <mode>       add: host (default) | vm (Tart, Apple Silicon) | container (Docker)
  --vm-image <image>       add: base image for vm/container isolation
  --refresh <policy>       add: never (default) | per-job | interval like 24h
  --yes                    Skip confirmations (non-interactive)

Config and runner instances live in ${pc.cyan("~/.local-worker")} — the repo itself stays clean.
`;

export async function main(argv) {
  const { args, flags } = parseArgv(argv);
  const cmd = args[0];

  if (cmd && !["help", "--help"].includes(cmd)) assertPlatform();

  switch (cmd) {
    case "setup": return setup(flags);
    case "add": return add(args[1], flags);
    case "remove": return remove(args[1], flags);
    case "status": return status(flags);
    case "doctor": return doctor(flags);
    case "watchdog": return watchdog(args[1], flags);
    case "vm": return vm(args[1], args[2], flags);
    case "integrate": return integrate(args[1], flags);
    case "logs": return logs(args[1], flags);
    case undefined:
    case "help":
    case "--help":
      console.log(HELP);
      return;
    default:
      console.error(`Unknown command: ${cmd}`);
      console.log(HELP);
      process.exitCode = 1;
  }
}

// Flags that take a value; everything else is boolean.
const VALUE_FLAGS = new Set(["labels", "name", "interval", "healthchecks", "isolation", "vm-image", "refresh"]);

function parseArgv(argv) {
  const args = [];
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) {
      args.push(a);
      continue;
    }
    const eq = a.indexOf("=");
    if (eq !== -1) {
      flags[a.slice(2, eq)] = a.slice(eq + 1);
    } else {
      const key = a.slice(2);
      if (VALUE_FLAGS.has(key) && argv[i + 1] !== undefined) {
        flags[key] = argv[++i];
      } else {
        flags[key] = true;
      }
    }
  }
  return { args, flags };
}
