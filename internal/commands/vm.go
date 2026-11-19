package commands

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/container"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
	"github.com/ssukru/local-worker/internal/vm"
)

// VM manages isolated (VM/container) runner environments.
func VM(sub, repoArg string, flags *Flags) error {
	switch sub {
	case "daemon":
		return vmDaemon(repoArg, flags) // internal: run by the service manager
	case "status":
		return vmStatus()
	case "refresh":
		return vmRefresh(repoArg, flags)
	default:
		fmt.Println(
			"Usage: lw vm <status|refresh owner/repo [--slot N]>\n" +
				"  status            Show isolated runners (VMs / containers)\n" +
				"  refresh           Discard an isolated environment now; a clean one is rebuilt\n" +
				"                    (all of a repo's runners unless --slot is given)")
		return nil
	}
}

// slotFlag parses --slot; returns 0 when absent, meaning "all slots".
func slotFlag(flags *Flags) (int, error) {
	if !flags.Has("slot") {
		return 0, nil
	}
	n, err := strconv.Atoi(flags.Get("slot"))
	if err != nil || n < 1 || n > util.MaxSlots {
		return 0, fmt.Errorf("--slot must be a number between 1 and %d", util.MaxSlots)
	}
	return n, nil
}

func vmDaemon(repo string, flags *Flags) error {
	if repo == "" {
		return errors.New("Usage: lw vm daemon owner/repo [--slot N]")
	}
	slot, err := slotFlag(flags)
	if err != nil {
		return err
	}
	if slot == 0 {
		slot = 1
	}
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil {
		return fmt.Errorf("%s is not configured", repo)
	}
	if slot > entry.Slots() {
		return fmt.Errorf("%s has %d runner(s) — no slot %d", repo, entry.Slots(), slot)
	}
	if entry.Isolation == "container" {
		return container.Daemon(repo, slot)
	}
	return vm.Daemon(repo, slot)
}

func isolatedRunners() []config.Runner {
	var out []config.Runner
	for _, r := range config.Load().Runners {
		if r.Isolated() {
			out = append(out, r)
		}
	}
	return out
}

func vmStatus() error {
	ui.Intro(" local-worker · vm status ")
	entries := isolatedRunners()
	if len(entries) == 0 {
		ui.Info("No isolated runners. Attach one with: lw add owner/repo --isolation vm|container")
		ui.Outro("")
		return nil
	}
	svc := service.Get()
	for _, r := range entries {
		backend := "tart"
		if r.Isolation == "container" {
			backend = "docker"
		}
		msg := ui.Bold(r.Repo) + "\n" +
			"  isolation   " + r.Isolation + " (" + backend + ")\n" +
			"  image       " + r.VM.Image + "\n" +
			"  refresh     " + r.VM.Refresh + "\n"
		if r.Slots() > 1 {
			msg += fmt.Sprintf("  concurrency %d environments · up to %d jobs at once\n", r.Slots(), r.Slots())
		}

		// One environment per slot — each gets its own state file.
		for slot := 1; slot <= r.Slots(); slot++ {
			name := vm.Name(r.Repo, slot)
			if r.Isolation == "container" {
				name = container.Name(r.Repo, slot)
			}
			state := vm.ReadState(name)
			daemonUp := svc.IsLoaded(util.RunnerLabel(r.Repo, slot))

			line := "  " + name + "\n" +
				"    daemon    " + upDown(daemonUp, "running", "not running")
			if r.Isolation == "vm" {
				env := ui.Yellow("down")
				if vm.Running(name) {
					env = ui.Green("up")
				}
				line += "   env " + env
			}
			if state != nil {
				line += fmt.Sprintf("\n    created   %s · jobs run: %d", state.CreatedAt, state.JobsRun)
			}
			msg += line + "\n"
		}
		ui.Message(strings.TrimRight(msg, "\n"))
	}
	ui.Outro("")
	return nil
}

func vmRefresh(repo string, flags *Flags) error {
	if repo == "" {
		fmt.Fprintln(os.Stderr, "Usage: lw vm refresh owner/repo [--slot N]")
		os.Exit(1)
	}
	slot, err := slotFlag(flags)
	if err != nil {
		return err
	}
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil || entry.VM == nil {
		fmt.Fprintf(os.Stderr, "%s has no isolated runner.\n", repo)
		os.Exit(1)
	}
	if slot > entry.Slots() {
		fmt.Fprintf(os.Stderr, "%s has %d runner(s) — no slot %d.\n", repo, entry.Slots(), slot)
		os.Exit(1)
	}

	// No --slot refreshes every environment for the repo. A busy slot finishes
	// its current job first — the daemon rebuilds on its next cycle.
	first, last := 1, entry.Slots()
	if slot > 0 {
		first, last = slot, slot
	}
	for s := first; s <= last; s++ {
		name := vm.Name(repo, s)
		if entry.Isolation == "container" {
			name = container.Name(repo, s)
		}
		state := vm.ReadState(name)
		if state == nil {
			state = &vm.State{CreatedAt: util.Timestamp()}
		}
		state.RecreateRequested = true
		if err := vm.WriteState(name, state); err != nil {
			return err
		}
		service.Get().Kickstart(util.RunnerLabel(repo, s))
		fmt.Printf("%s marked for rebuild — the daemon is recreating it from %s.\n", name, entry.VM.Image)
	}
	return nil
}
