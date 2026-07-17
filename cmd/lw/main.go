// lw — turn this machine into a self-hosted GitHub Actions runner fleet.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ssukru/local-worker/internal/commands"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
)

// Overridden at release time via -ldflags "-X main.version=…".
var version = "dev"

func help() string {
	return `
` + ui.Bold("local-worker") + ` — turn this machine into a self-hosted GitHub Actions runner fleet

` + ui.Bold("Usage:") + ` lw <command> [args]

` + ui.Bold("Commands:") + `
  setup                    Guided first-time setup (deps, auth, repos, watchdog)
  add [owner/repo]         Attach this machine as a runner for a repo
  remove <owner/repo>      Detach a runner and clean up
  status                   Local + GitHub view of all runners
  doctor                   Diagnose machine readiness (power, tools, network)
  watchdog <install|uninstall|check|status>
                           Self-healing health checks + dead-man's-switch ping
  vm <status|refresh>      Manage isolated (VM/container) runner environments
  integrate <owner/repo>   Print the workflow changes to route jobs here
  logs [owner/repo]        Tail runner / watchdog logs

` + ui.Bold("Common flags:") + `
  --labels a,b,c           Extra runner labels (add)
  --isolation <mode>       add: host (default) | vm (Tart, Apple Silicon) | container (Docker)
  --vm-image <image>       add: base image for vm/container isolation
  --refresh <policy>       add: never (default) | per-job | interval like 24h
  --yes                    Skip confirmations (non-interactive)

Config and runner instances live in ` + ui.Cyan("~/.local-worker") + ` — the repo itself stays clean.
`
}

// valueFlags take a value; everything else is boolean.
var valueFlags = map[string]bool{
	"labels": true, "name": true, "interval": true, "healthchecks": true,
	"isolation": true, "vm-image": true, "refresh": true,
}

func parseArgv(argv []string) ([]string, *commands.Flags) {
	var args []string
	flags := commands.NewFlags()
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "--") {
			args = append(args, a)
			continue
		}
		if eq := strings.Index(a, "="); eq != -1 {
			flags.Set(a[2:eq], a[eq+1:])
			continue
		}
		key := a[2:]
		if valueFlags[key] && i+1 < len(argv) {
			i++
			flags.Set(key, argv[i])
		} else {
			flags.Set(key, "")
		}
	}
	return args, flags
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func run() error {
	args, flags := parseArgv(os.Args[1:])
	cmd := arg(args, 0)

	if cmd != "" && cmd != "help" && cmd != "--help" && cmd != "version" && cmd != "--version" {
		if err := service.AssertPlatform(); err != nil {
			return err
		}
	}

	switch cmd {
	case "setup":
		return commands.Setup(flags)
	case "add":
		return commands.Add(arg(args, 1), flags, false)
	case "remove":
		return commands.Remove(arg(args, 1), flags)
	case "status":
		return commands.Status(flags)
	case "doctor":
		return commands.Doctor(flags)
	case "watchdog":
		return commands.Watchdog(arg(args, 1), flags)
	case "vm":
		return commands.VM(arg(args, 1), arg(args, 2), flags)
	case "integrate":
		return commands.Integrate(arg(args, 1), flags)
	case "logs":
		return commands.Logs(arg(args, 1), flags)
	case "version", "--version":
		fmt.Println("lw " + version)
		return nil
	case "", "help", "--help":
		fmt.Println(help())
		return nil
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		fmt.Println(help())
		os.Exit(1)
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
