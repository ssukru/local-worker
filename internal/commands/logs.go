package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/util"
)

// Logs tails a runner's (or the watchdog's) log file. Each runner slot has its
// own log; --slot picks one (default 1).
func Logs(target string, flags *Flags) error {
	cfg := config.Load()
	slot := 1
	if flags.Has("slot") {
		n, err := strconv.Atoi(flags.Get("slot"))
		if err != nil || n < 1 || n > util.MaxSlots {
			fmt.Fprintf(os.Stderr, "--slot must be a number between 1 and %d.\n", util.MaxSlots)
			os.Exit(1)
		}
		slot = n
	}

	// An autoscaled repo has one combined log (the daemon's), with each line
	// tagged by the runner that produced it — slots come and go, so per-slot
	// log files would scatter a single job's story across files.
	logLabel := func(repo string) string {
		if entry := config.FindRunner(&cfg, repo); entry != nil && entry.Auto() {
			return util.PoolLabel(repo)
		}
		return util.RunnerLabel(repo, slot)
	}

	var label string
	if target == "" || target == "watchdog" {
		if target == "watchdog" || len(cfg.Runners) == 0 {
			label = util.WatchdogLabel
		} else {
			label = logLabel(cfg.Runners[0].Repo)
		}
	} else {
		label = logLabel(target)
	}
	file := filepath.Join(util.LogsDir(), label+".log")
	if _, err := os.Stat(file); err != nil {
		fmt.Fprintf(os.Stderr, "No log file yet: %s\n", file)
		if entry := config.FindRunner(&cfg, target); entry != nil && slot > entry.Slots() {
			fmt.Fprintf(os.Stderr, "%s has %d runner(s) — valid slots are 1..%d.\n",
				target, entry.Slots(), entry.Slots())
		}
		os.Exit(1)
	}
	return util.Run("tail", "-n", "100", "-f", file)
}
