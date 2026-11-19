package commands

import (
	"fmt"
	"strings"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/pool"
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
	for i := range cfg.Runners {
		r := cfg.Runners[i]
		isolated := r.Isolated()
		remote := runner.Remote(r.Repo)

		if r.Auto() {
			ui.Message(autoStatus(&r, svc, remote))
			continue
		}

		slots := r.Slots()
		msg := ui.Bold(r.Repo) + "\n"
		if slots == 1 {
			msg += "  runner name   " + r.Name + "\n"
		} else {
			msg += fmt.Sprintf("  concurrency   %d runners · up to %d jobs at once\n", slots, slots)
		}
		msg += "  labels        self-hosted, " + util.OsLabel() + " + " + joinLabels(r.Labels) + "\n"
		if isolated {
			msg += "  isolation     " + r.Isolation + " · refresh " + r.VM.Refresh + " (details: lw vm status)\n"
		}

		for slot := 1; slot <= slots; slot++ {
			serviceLoaded := svc.IsLoaded(util.RunnerLabel(r.Repo, slot))
			// Isolated runners live inside a VM/container; the daemon being up
			// is the local signal, GitHub is the authority on the runner itself.
			listening := isolated || runner.ListenerRunning(r.Repo, slot)
			mine := runner.FindRemote(remote, r.SlotName(slot))

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

			local := "  service       " + upDown(serviceLoaded, "loaded", "not loaded") + "   " +
				"listener " + upDown(listening, "running", "stopped") + "\n"
			if isolated {
				local = "  daemon        " + upDown(serviceLoaded, "running", "not running") + "\n"
			}
			if slots == 1 {
				msg += local + "  github sees   " + github
				break
			}
			// Multi-runner: one compact line per slot.
			daemonOrSvc := upDown(serviceLoaded && listening, "up", "DOWN")
			msg += fmt.Sprintf("  #%-2d %-28s %s · %s\n", slot, r.SlotName(slot), daemonOrSvc, github)
		}
		ui.Message(strings.TrimRight(msg, "\n"))
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

// autoStatus renders an autoscaling pool. The interesting numbers are the
// pool's own (how many runners exist right now, how many are working), not a
// per-slot service list — slots come and go on their own.
func autoStatus(r *config.Runner, svc service.Backend, remote []runner.RemoteRunner) string {
	daemonUp := svc.IsLoaded(util.PoolLabel(r.Repo))

	msg := ui.Bold(r.Repo) + "\n" +
		fmt.Sprintf("  scaling       %s · %d-%d runners on demand\n",
			ui.Green("auto"), r.MinRunners(), r.MaxRunners()) +
		"  labels        self-hosted, " + util.OsLabel() + " + " + joinLabels(r.Labels) + "\n" +
		"  autoscaler    " + upDown(daemonUp, "running", "not running") + "\n"

	st := pool.ReadState(r.Repo)
	switch {
	case !daemonUp:
		msg += "  pool          " + ui.Red("stopped") + " — restart with: lw add " + r.Repo
	case st == nil:
		msg += "  pool          " + ui.Dim("starting up…")
	default:
		state := fmt.Sprintf("%d runner(s) up · %d running a job", st.Live, st.Busy)
		if st.Queued > 0 {
			state += fmt.Sprintf(" · %s", ui.Cyan(fmt.Sprintf("%d job(s) queued", st.Queued)))
		}
		if st.Live == 0 && st.Queued == 0 {
			state = ui.Dim("idle — runners start when a job queues")
		}
		msg += "  pool          " + state
		if st.LastError != "" {
			msg += "\n  last error    " + ui.Yellow(st.LastError)
		}
	}

	// GitHub's own count is the cross-check: ephemeral runners register and
	// deregister constantly, so a mismatch here is normal mid-cycle.
	if remote != nil {
		mine := 0
		for slot := 1; slot <= r.MaxRunners(); slot++ {
			if runner.FindRemote(remote, r.SlotName(slot)) != nil {
				mine++
			}
		}
		msg += fmt.Sprintf("\n  github sees   %d registered", mine)
	}
	return msg
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
