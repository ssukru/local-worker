package commands

import (
	"os"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/container"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
	"github.com/ssukru/local-worker/internal/vm"
)

// Remove detaches a runner and cleans up.
func Remove(repo string, flags *Flags) error {
	ui.Intro(" local-worker · remove runner ")
	cfg := config.Load()

	if repo == "" {
		if !util.IsTTY() || len(cfg.Runners) == 0 {
			ui.Error("Usage: lw remove owner/repo")
			os.Exit(1)
		}
		options := make([]ui.SelectOption, len(cfg.Runners))
		for i, r := range cfg.Runners {
			options[i] = ui.SelectOption{Value: r.Repo, Label: r.Repo}
		}
		choice, err := ui.Select("Which runner should be removed?", options)
		if err != nil {
			ui.Cancel("Cancelled.")
			return nil
		}
		repo = choice
	}

	entry := config.FindRunner(&cfg, repo)
	if entry == nil {
		ui.Warn(repo + " is not in the local config — cleaning up anyway.")
	}

	if util.IsTTY() && !flags.Bool("yes") {
		ok, err := ui.Confirm("Deregister this machine from "+repo+" and delete its work directory?", true)
		if err != nil || !ok {
			ui.Cancel("Cancelled.")
			return nil
		}
	}

	var s ui.Spinner
	s.Start("Stopping service")
	runner.UninstallService(repo)
	s.Stop("Service removed")

	if entry != nil && entry.Isolated() {
		s.Start("Discarding " + entry.Isolation + " environment and deregistering")
		name := vm.Name(repo)
		if entry.Isolation == "container" {
			name = container.Name(repo)
			container.Delete(name)
		} else {
			vm.Delete(name)
		}
		vm.ClearState(name)
		vm.ForceDeregister(repo, entry.Name)
		s.Stop("Environment discarded, runner deregistered")
	} else {
		s.Start("Deregistering from GitHub and deleting local files")
		runner.Deregister(repo)
		s.Stop("Deregistered")
	}

	config.RemoveRunner(&cfg, repo)
	if err := config.Save(cfg); err != nil {
		return err
	}
	ui.Outro(repo + " detached.")
	return nil
}
