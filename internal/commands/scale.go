package commands

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/pool"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
)

// Scale adjusts an autoscaling pool's bounds. The daemon re-reads its config
// every poll, so this takes effect within seconds without restarting anything
// or disturbing jobs in flight.
func Scale(repo, maxArg string, flags *Flags) error {
	cfg := config.Load()

	if repo == "" {
		if !util.IsTTY() || len(cfg.Runners) == 0 {
			ui.Error("Usage: lw scale owner/repo [max] [--min N] [--max N]")
			os.Exit(1)
		}
		options := make([]ui.SelectOption, len(cfg.Runners))
		for i, r := range cfg.Runners {
			options[i] = ui.SelectOption{Value: r.Repo, Label: r.Repo, Hint: poolSummary(&cfg.Runners[i])}
		}
		choice, err := ui.Select("Which runner pool should be resized?", options)
		if err != nil {
			ui.Cancel("Cancelled.")
			return nil
		}
		repo = choice
	}

	entry := config.FindRunner(&cfg, repo)
	if entry == nil {
		ui.Error(repo + " is not attached. Run: lw add " + repo)
		os.Exit(1)
	}
	if !entry.Auto() {
		ui.Error(repo + " uses a fixed pool of " + strconv.Itoa(entry.Slots()) + " runner(s).\n" +
			"Resize it with:        lw add " + repo + " --count N\n" +
			"Or switch to autoscaling: lw add " + repo + " --min 1 --max " +
			strconv.Itoa(util.DefaultMaxRunners()))
		os.Exit(1)
	}

	min, max := entry.MinRunners(), entry.MaxRunners()
	changed := false

	// Positional shorthand: `lw scale owner/repo 8` sets the ceiling.
	if maxArg != "" {
		n, err := strconv.Atoi(maxArg)
		if err != nil || n < 1 || n > util.MaxSlots {
			ui.Error(fmt.Sprintf("Max must be a number between 1 and %d.", util.MaxSlots))
			os.Exit(1)
		}
		max = n
		changed = true
	}
	if flags.Has("min") {
		min = intFlag(flags, "min", 0, util.MaxSlots)
		changed = true
	}
	if flags.Has("max") {
		max = intFlag(flags, "max", 1, util.MaxSlots)
		changed = true
	}

	if !changed {
		// No arguments: report instead of guessing what the user meant.
		ui.Intro(" local-worker · scale ")
		ui.Message(ui.Bold(repo) + "\n  " + poolSummary(entry) + "\n" + livePoolLine(repo))
		ui.Outro("Resize with: " + ui.Cyan("lw scale "+repo+" --max 10"))
		return nil
	}
	if min > max {
		ui.Error(fmt.Sprintf("--min (%d) cannot exceed --max (%d).", min, max))
		os.Exit(1)
	}

	entry.Scale.Min, entry.Scale.Max = min, max
	if err := config.Save(cfg); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("%s now scales between %d and %d runners.", repo, min, max))
	ui.Info("The autoscaler picks this up within a few seconds — running jobs are untouched.")
	return nil
}

func poolSummary(r *config.Runner) string {
	if !r.Auto() {
		return fmt.Sprintf("fixed · %d runner(s)", r.Slots())
	}
	return fmt.Sprintf("auto · %d-%d runners", r.MinRunners(), r.MaxRunners())
}

// livePoolLine reports what the daemon last observed, or why it can't.
func livePoolLine(repo string) string {
	st := pool.ReadState(repo)
	if st == nil {
		return "  live        " + ui.Dim("no report yet — is the autoscaler running? (lw status)")
	}
	line := fmt.Sprintf("  live        %d running · %d busy · %d queued  (as of %s)",
		st.Live, st.Busy, st.Queued, st.UpdatedAt)
	if st.LastError != "" {
		line += "\n  last error  " + ui.Yellow(st.LastError)
	}
	return line
}
