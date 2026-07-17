package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/util"
)

// Logs tails a runner's (or the watchdog's) log file.
func Logs(target string, flags *Flags) error {
	cfg := config.Load()
	var label string
	if target == "" || target == "watchdog" {
		if target == "watchdog" || len(cfg.Runners) == 0 {
			label = util.WatchdogLabel
		} else {
			label = util.RunnerLabel(cfg.Runners[0].Repo)
		}
	} else {
		label = util.RunnerLabel(target)
	}
	file := filepath.Join(util.LogsDir(), label+".log")
	if _, err := os.Stat(file); err != nil {
		fmt.Fprintf(os.Stderr, "No log file yet: %s\n", file)
		os.Exit(1)
	}
	return util.Run("tail", "-n", "100", "-f", file)
}
