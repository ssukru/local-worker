package commands

import (
	"fmt"
	"os"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/container"
	"github.com/ssukru/local-worker/internal/pool"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/service"
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
			hint := ""
			if r.Slots() > 1 {
				hint = fmt.Sprintf("%d runners", r.Slots())
			}
			options[i] = ui.SelectOption{Value: r.Repo, Label: r.Repo, Hint: hint}
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
		what := "its work directory"
		if entry != nil && entry.Slots() > 1 {
			what = fmt.Sprintf("all %d runners and their work directories", entry.Slots())
		}
		ok, err := ui.Confirm("Deregister this machine from "+repo+" and delete "+what+"?", true)
		if err != nil || !ok {
			ui.Cancel("Cancelled.")
			return nil
		}
	}

	var s0 ui.Spinner
	if entry != nil && entry.Auto() {
		// Stop the autoscaler before reaping slots, or it would cheerfully
		// spawn replacements for the runners we're removing.
		s0.Start("Stopping the autoscaler")
		service.Get().Uninstall(util.PoolLabel(repo))
		pool.ClearState(repo)
		s0.Stop("Autoscaler stopped")
	}

	// Remove every slot. Probe past the recorded count so instances left by an
	// interrupted scale-down get cleaned up too.
	slots := 1
	if entry != nil {
		slots = entry.TopSlot()
	}
	slots = runner.LocalSlots(repo, slots, func(dir string) bool {
		return !ownedByAnotherRepo(repo, dir)
	})

	var s ui.Spinner
	for slot := 1; slot <= slots; slot++ {
		of := ""
		if slots > 1 {
			of = fmt.Sprintf(" #%d/%d", slot, slots)
		}
		s.Start("Stopping service" + of)
		runner.UninstallService(repo, slot)
		s.Stop("Service" + of + " removed")

		if entry != nil && entry.Isolated() {
			s.Start("Discarding " + entry.Isolation + " environment" + of + " and deregistering")
			name := vm.Name(repo, slot)
			if entry.Isolation == "container" {
				name = container.Name(repo, slot)
				container.Delete(name)
			} else {
				vm.Delete(name)
			}
			vm.ClearState(name)
			vm.ForceDeregister(repo, entry.SlotName(slot))
			s.Stop("Environment" + of + " discarded, runner deregistered")
		} else {
			s.Start("Deregistering" + of + " from GitHub and deleting local files")
			runner.Deregister(repo, slot)
			s.Stop("Deregistered" + of)
		}
	}

	config.RemoveRunner(&cfg, repo)
	if err := config.Save(cfg); err != nil {
		return err
	}
	ui.Outro(repo + " detached.")
	return nil
}
