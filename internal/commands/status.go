package commands

import (
	"fmt"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
)

// Status shows the local + GitHub view of all runners.
func Status(flags *Flags) error {
	cfg := config.Load()
	ui.Intro(" local-worker · status ")

	if len(cfg.Runners) == 0 {
		ui.Info("No runners attached yet. Run `lw setup` or `lw add owner/repo`.")
		ui.Outro("")
		return nil
	}

	svc := service.Get()
	for _, r := range cfg.Runners {
		isolated := r.Isolated()
		serviceLoaded := svc.IsLoaded(util.RunnerLabel(r.Repo))
		listening := isolated || runner.ListenerRunning(r.Repo)
		remote := runner.Remote(r.Repo)
		mine := runner.FindRemote(remote, r.Name)

		var github string
		switch {
		case remote == nil:
			github = ui.Yellow("unknown (no network/access)")
		case mine == nil:
			github = ui.Red("NOT REGISTERED")
		case mine.Status == "online" && mine.Busy:
			github = ui.Cyan("online · running a job")
		case mine.Status == "online":
			github = ui.Green("online · idle")
		default:
			github = ui.Red("offline")
		}

		msg := ui.Bold(r.Repo) + "\n" +
			"  runner name   " + r.Name + "\n" +
			"  labels        self-hosted, " + util.OsLabel() + " + " + joinLabels(r.Labels) + "\n"
		if isolated {
			msg += "  isolation     " + r.Isolation + " · refresh " + r.VM.Refresh + " (details: lw vm status)\n" +
				"  daemon        " + upDown(serviceLoaded, "running", "not running") + "\n"
		} else {
			msg += "  service       " + upDown(serviceLoaded, "loaded", "not loaded") + "   " +
				"listener " + upDown(listening, "running", "stopped") + "\n"
		}
		msg += "  github sees   " + github
		ui.Message(msg)
	}

	watchdogState := ui.Dim("not installed")
	if cfg.Watchdog.Installed {
		if svc.IsLoaded(util.WatchdogLabel) {
			watchdogState = ui.Green(fmt.Sprintf("active (every %ds)", cfg.Watchdog.IntervalSeconds))
		} else {
			watchdogState = ui.Red("installed but not loaded")
		}
	}
	ui.Message(ui.Bold("watchdog") + "      " + watchdogState)
	ui.Outro("")
	return nil
}

func joinLabels(labels []string) string {
	out := ""
	for i, l := range labels {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out
}

func upDown(ok bool, yes, no string) string {
	if ok {
		return ui.Green(yes)
	}
	return ui.Red(no)
}
