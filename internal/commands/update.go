package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ssukru/local-worker/internal/ui"
	updater "github.com/ssukru/local-worker/internal/update"
	"github.com/ssukru/local-worker/internal/util"
)

type updateEngine interface {
	Plan(context.Context, string, string, string) (updater.Plan, error)
	Apply(context.Context, updater.Plan) error
}

var updateFactory = func() updateEngine { return updater.NewUpdater() }

func updateHelp() string {
	return `Usage: lw update [flags]

Check for and install a verified lw release.

Flags:
  --check          Only report whether an update is available
  --to <version>   Install an exact release (0.3.0 or v0.3.0)
  --force          Allow reinstall, downgrade, or replacing a dev build
  --yes            Skip confirmation (required when non-interactive)
  --help           Show this help
`
}

func Update(currentVersion, argv0 string, args []string, flags *Flags) error {
	if flags.Has("help") {
		fmt.Print(updateHelp())
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("lw update does not accept positional arguments")
	}
	allowed := map[string]bool{"check": true, "to": true, "force": true, "yes": true}
	for _, key := range flags.Keys() {
		if !allowed[key] {
			return fmt.Errorf("unknown flag for lw update: --%s", key)
		}
	}
	requested := flags.Get("to")
	if flags.Has("to") && (requested == "" || strings.HasPrefix(requested, "--")) {
		return fmt.Errorf("--to requires a semantic version")
	}

	ctx := context.Background()
	engine := updateFactory()
	plan, err := engine.Plan(ctx, currentVersion, requested, argv0)
	if err != nil {
		return err
	}

	ui.Intro("lw update")
	ui.Info(fmt.Sprintf("Current: %s\nTarget:  %s\nBinary:  %s", plan.CurrentVersion, plan.TargetVersion, plan.Executable.InvocationPath))
	if plan.Executable.Manager == updater.ManagerHomebrew {
		ui.Warn("This installation is managed by Homebrew.\nRun: brew update && brew upgrade local-worker")
		ui.Outro("")
		if flags.Bool("check") {
			return nil
		}
		return fmt.Errorf("cannot self-update a Homebrew-managed binary")
	}

	if flags.Bool("check") {
		switch {
		case plan.Development:
			ui.Warn("This is an unstamped development build; use --force to replace it with a release.")
		case plan.Comparison < 0:
			ui.Success(fmt.Sprintf("Update available: %s → %s", plan.CurrentVersion, plan.TargetVersion))
		case plan.Comparison == 0:
			ui.Success(fmt.Sprintf("lw %s is up to date.", plan.CurrentVersion))
		default:
			ui.Info(fmt.Sprintf("Current build %s is newer than %s.", plan.CurrentVersion, plan.TargetVersion))
		}
		ui.Outro("")
		return nil
	}

	force := flags.Bool("force")
	if plan.Development && !force {
		return fmt.Errorf("refusing to replace a development build without --force")
	}
	if !plan.Development {
		switch {
		case plan.Comparison == 0 && !force:
			ui.Outro(fmt.Sprintf("lw %s is already up to date.", plan.CurrentVersion))
			return nil
		case plan.Comparison > 0 && !force:
			return fmt.Errorf("target %s is older than current %s; use --force to downgrade", plan.TargetVersion, plan.CurrentVersion)
		}
	}

	if !flags.Bool("yes") {
		if !util.IsTTY() {
			return fmt.Errorf("non-interactive updates require --yes")
		}
		confirmed, err := ui.Confirm(fmt.Sprintf("Install lw %s?", plan.TargetVersion), true)
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				ui.Cancel("Update cancelled.")
				return nil
			}
			return err
		}
		if !confirmed {
			ui.Cancel("Update cancelled.")
			return nil
		}
	}

	ui.Step("Downloading and verifying the release")
	if err := engine.Apply(ctx, plan); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Updated lw %s → %s", plan.CurrentVersion, plan.TargetVersion))
	ui.Warn("Running VM/container daemons were not restarted; they will use the new binary after their next restart.")
	ui.Outro("Done.")
	return nil
}
