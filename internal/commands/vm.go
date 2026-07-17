package commands

import (
	"errors"
	"fmt"
	"os"

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
		return vmDaemon(repoArg) // internal: run by the service manager
	case "status":
		return vmStatus()
	case "refresh":
		return vmRefresh(repoArg)
	default:
		fmt.Println(
			"Usage: lw vm <status|refresh owner/repo>\n" +
				"  status            Show isolated runners (VMs / containers)\n" +
				"  refresh           Discard an isolated environment now; a clean one is rebuilt")
		return nil
	}
}

func vmDaemon(repo string) error {
	if repo == "" {
		return errors.New("Usage: lw vm daemon owner/repo")
	}
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil {
		return fmt.Errorf("%s is not configured", repo)
	}
	if entry.Isolation == "container" {
		return container.Daemon(repo)
	}
	return vm.Daemon(repo)
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
		name := vm.Name(r.Repo)
		backend := "tart"
		if r.Isolation == "container" {
			name = container.Name(r.Repo)
			backend = "docker"
		}
		state := vm.ReadState(name)
		daemonUp := svc.IsLoaded(util.RunnerLabel(r.Repo))

		msg := ui.Bold(r.Repo) + "\n" +
			"  isolation   " + r.Isolation + " (" + backend + ")\n" +
			"  image       " + r.VM.Image + "\n" +
			"  refresh     " + r.VM.Refresh + "\n" +
			"  daemon      " + upDown(daemonUp, "running", "not running")
		if r.Isolation == "vm" {
			env := ui.Yellow("down")
			if vm.Running(name) {
				env = ui.Green("up")
			}
			msg += "   env " + env
		}
		if state != nil {
			msg += fmt.Sprintf("\n  created     %s · jobs run: %d", state.CreatedAt, state.JobsRun)
		}
		ui.Message(msg)
	}
	ui.Outro("")
	return nil
}

func vmRefresh(repo string) error {
	if repo == "" {
		fmt.Fprintln(os.Stderr, "Usage: lw vm refresh owner/repo")
		os.Exit(1)
	}
	cfg := config.Load()
	entry := config.FindRunner(&cfg, repo)
	if entry == nil || entry.VM == nil {
		fmt.Fprintf(os.Stderr, "%s has no isolated runner.\n", repo)
		os.Exit(1)
	}
	name := vm.Name(repo)
	if entry.Isolation == "container" {
		name = container.Name(repo)
	}
	state := vm.ReadState(name)
	if state == nil {
		state = &vm.State{CreatedAt: util.Timestamp()}
	}
	state.RecreateRequested = true
	if err := vm.WriteState(name, state); err != nil {
		return err
	}
	service.Get().Kickstart(util.RunnerLabel(repo))
	fmt.Printf("%s marked for rebuild — the daemon is recreating it from %s.\n", name, entry.VM.Image)
	return nil
}
