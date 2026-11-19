package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/runner"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
)

// Watchdog manages the self-healing health check service.
func Watchdog(sub string, flags *Flags) error {
	switch sub {
	case "install":
		return watchdogInstall(flags)
	case "uninstall":
		return watchdogUninstall()
	case "check":
		return watchdogCheck()
	default:
		if service.Get().IsLoaded(util.WatchdogLabel) {
			fmt.Println("watchdog: active")
		} else {
			fmt.Println("watchdog: not running (lw watchdog install)")
		}
		return nil
	}
}

func watchdogInstall(flags *Flags) error {
	ui.Intro(" local-worker · watchdog ")
	cfg := config.Load()

	healthchecksURL := cfg.Watchdog.HealthchecksURL
	if flags.Has("healthchecks") {
		healthchecksURL = flags.Get("healthchecks")
	} else if util.IsTTY() {
		ui.Info("The watchdog restarts dead runners and pings a dead-man's-switch URL.\n" +
			"If this machine goes fully offline, the missing pings trigger the alert —\n" +
			"create a free check at https://healthchecks.io and paste its ping URL.")
		url, err := ui.Text("Healthchecks.io ping URL (Enter to skip):", "https://hc-ping.com/…", healthchecksURL, nil)
		if err != nil {
			ui.Cancel("Cancelled.")
			return nil
		}
		healthchecksURL = strings.TrimSpace(url)
	}

	interval := cfg.Watchdog.IntervalSeconds
	if flags.Has("interval") {
		if v, err := strconv.Atoi(flags.Get("interval")); err == nil && v > 0 {
			interval = v
		} else {
			interval = 300
		}
	}
	if interval == 0 {
		interval = 300
	}
	cfg.Watchdog.Installed = true
	cfg.Watchdog.IntervalSeconds = interval
	cfg.Watchdog.HealthchecksURL = healthchecksURL
	if err := config.Save(cfg); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	svc := service.Get()
	svc.Uninstall(util.WatchdogLabel)
	if err := svc.Install(service.Spec{
		Label:         util.WatchdogLabel,
		ProgramArgs:   []string{exe, "watchdog", "check"},
		LogPath:       filepath.Join(util.LogsDir(), util.WatchdogLabel+".log"),
		StartInterval: interval,
	}); err != nil {
		return err
	}
	alerts := ""
	if healthchecksURL != "" {
		alerts = " + offline alerts via healthchecks.io"
	}
	ui.Outro(fmt.Sprintf("Watchdog active — every %ds%s.", interval, alerts))
	return nil
}

func watchdogUninstall() error {
	cfg := config.Load()
	service.Get().Uninstall(util.WatchdogLabel)
	cfg.Watchdog.Installed = false
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Watchdog removed.")
	return nil
}

// watchdogCheck runs one health pass. Runs headless from the service manager
// — plain output only.
func watchdogCheck() error {
	cfg := config.Load()
	var report []string
	svc := service.Get()

	for _, r := range cfg.Runners {
		if r.Auto() {
			// The autoscaler owns its runners' lifecycle — individual runners
			// exiting is normal, so the thing to health-check is the daemon.
			if svc.IsLoaded(util.PoolLabel(r.Repo)) {
				report = append(report, r.Repo+": autoscaler ok")
				continue
			}
			kicked := svc.Kickstart(util.PoolLabel(r.Repo))
			verdict := "ok"
			if !kicked {
				verdict = "FAILED"
			}
			report = append(report, r.Repo+": autoscaler DOWN — kickstart "+verdict)
			notify(&cfg, "local-worker: autoscaler for "+r.Repo+" was down — restart "+verdict)
			continue
		}
		if r.Isolated() {
			// Isolated runners live inside a VM/container managed by their
			// daemon; the service manager's restart policy covers the daemon
			// itself.
			continue
		}
		// Each slot is its own listener and its own service — heal them
		// independently, so one dead runner doesn't mask the healthy ones.
		alive := 0
		for slot := 1; slot <= r.Slots(); slot++ {
			if runner.ListenerRunning(r.Repo, slot) {
				alive++
				continue
			}
			// Listener is dead: kick the service (its own restart policy usually
			// beats us).
			kicked := svc.Kickstart(util.RunnerLabel(r.Repo, slot))
			verdict := "ok"
			if !kicked {
				verdict = "FAILED"
			}
			report = append(report, fmt.Sprintf("%s#%d: listener DOWN — kickstart %s", r.Repo, slot, verdict))
			outcome := "succeeded"
			if !kicked {
				outcome = "FAILED"
			}
			notify(&cfg, fmt.Sprintf("local-worker: runner %s (#%d of %d) was down — restart %s",
				r.Repo, slot, r.Slots(), outcome))
		}
		if alive > 0 {
			report = append(report, fmt.Sprintf("%s: %d/%d listeners ok", r.Repo, alive, r.Slots()))
		}
	}

	// GitHub's view (skippable if offline — that's what the dead-man ping
	// covers).
	for _, r := range cfg.Runners {
		if r.Auto() {
			// Ephemeral runners register and deregister constantly; "offline"
			// is their normal resting state, not a fault worth alerting on.
			continue
		}
		remote := runner.Remote(r.Repo)
		if remote == nil {
			continue
		}
		for slot := 1; slot <= r.Slots(); slot++ {
			if mine := runner.FindRemote(remote, r.SlotName(slot)); mine != nil && mine.Status != "online" {
				report = append(report, fmt.Sprintf("%s#%d: GitHub reports %s", r.Repo, slot, mine.Status))
			}
		}
	}

	if cfg.Watchdog.HealthchecksURL != "" {
		if _, ok := util.TrySh("curl", []string{"-fsS", "-m", "10", "-o", "/dev/null", cfg.Watchdog.HealthchecksURL}); ok {
			report = append(report, "dead-man ping sent")
		} else {
			report = append(report, "dead-man ping FAILED")
		}
	}

	fmt.Printf("[%s] %s\n", util.Timestamp(), strings.Join(report, " | "))
	return nil
}

func notify(cfg *config.Config, text string) {
	tg := cfg.Watchdog.Telegram
	if tg.BotToken == "" || tg.ChatID == "" {
		return
	}
	util.TrySh("curl", []string{
		"-fsS", "-m", "10", "-o", "/dev/null",
		"https://api.telegram.org/bot" + tg.BotToken + "/sendMessage",
		"-d", "chat_id=" + tg.ChatID,
		"--data-urlencode", "text=" + text,
	})
}
