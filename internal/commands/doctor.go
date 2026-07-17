package commands

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/ssukru/local-worker/internal/config"
	"github.com/ssukru/local-worker/internal/service"
	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
)

type checkLevel int

const (
	levelOK checkLevel = iota
	levelWarn
	levelBad
)

type check struct {
	level checkLevel
	msg   string
}

func ok(msg string) check   { return check{levelOK, msg} }
func warn(msg string) check { return check{levelWarn, msg} }
func bad(msg string) check  { return check{levelBad, msg} }

var sleepLineRe = regexp.MustCompile(`^\s*sleep\s`)

// CollectChecks probes machine readiness; forSetup skips the service checks.
func CollectChecks(forSetup bool) []check {
	var checks []check
	cfg := config.Load()
	mac := runtime.GOOS == "darwin"

	// Base tools
	if _, gitOK := util.TrySh("git", []string{"--version"}); gitOK {
		checks = append(checks, ok("git installed"))
	} else if mac {
		checks = append(checks, bad("git missing — install Xcode Command Line Tools: xcode-select --install"))
	} else {
		checks = append(checks, bad("git missing — install it with your package manager"))
	}
	if _, ghOK := util.TrySh("gh", []string{"--version"}); !ghOK {
		if mac {
			checks = append(checks, bad("GitHub CLI missing — brew install gh"))
		} else {
			checks = append(checks, bad("GitHub CLI missing — see https://github.com/cli/cli#installation"))
		}
	} else if !util.GhAuthed() {
		checks = append(checks, bad("GitHub CLI not authenticated — run: gh auth login"))
	} else {
		checks = append(checks, ok("GitHub CLI authenticated as "+util.GhLogin()))
	}

	// Machine
	arch := util.MachineArch()
	archNote := ""
	if mac && arch == "arm64" {
		archNote = " (Apple Silicon)"
	}
	checks = append(checks, ok("Architecture: "+arch+archNote))
	var osVersion string
	if mac {
		v, _ := util.TrySh("sw_vers", []string{"-productVersion"})
		osVersion = "macOS " + v
	} else {
		if pretty, prettyOK := util.TrySh("sh", []string{"-c", `. /etc/os-release && echo "$PRETTY_NAME"`}); prettyOK && pretty != "" {
			osVersion = pretty
		} else {
			kernel, _ := util.TrySh("uname", []string{"-r"})
			osVersion = strings.TrimSpace("Linux " + kernel)
		}
	}
	checks = append(checks, ok(osVersion))

	if freeGb, found := diskFreeGb(); found {
		if freeGb > 40 {
			checks = append(checks, ok(fmt.Sprintf("Disk free: %d GB", freeGb)))
		} else {
			checks = append(checks, warn(fmt.Sprintf("Disk free: %d GB — builds eat disk fast; keep >40 GB free", freeGb)))
		}
	}

	if mac {
		// Power management — the difference between a runner and a paperweight.
		pm, _ := util.TrySh("pmset", []string{"-g"})
		sleepLine := ""
		for _, l := range strings.Split(pm, "\n") {
			if sleepLineRe.MatchString(l) {
				sleepLine = l
				break
			}
		}
		sleepVal := -1
		if fields := strings.Fields(sleepLine); len(fields) > 1 {
			if v, err := strconv.Atoi(fields[1]); err == nil {
				sleepVal = v
			}
		}
		sleepPrevented := strings.Contains(sleepLine, "sleep prevented")
		if sleepVal == 0 || strings.Contains(pm, "SleepDisabled 1") {
			checks = append(checks, ok("System sleep disabled"))
		} else if sleepPrevented {
			checks = append(checks, warn(fmt.Sprintf("System sleep is %d min but currently prevented by an app — make it permanent: sudo pmset -a sleep 0", sleepVal)))
		} else {
			checks = append(checks, bad(fmt.Sprintf("System sleeps after %d min — jobs will stall. Fix: sudo pmset -a sleep 0 disksleep 0; sudo pmset -a disablesleep 1 (keeps a MacBook awake with the lid closed)", sleepVal)))
		}
		if !strings.Contains(pm, "autorestart          1") {
			checks = append(checks, warn("Auto-restart after power failure is off — fix: sudo pmset -a autorestart 1"))
		} else {
			checks = append(checks, ok("Auto-restart after power failure enabled"))
		}

		// Auto-login: launchd agents (and GUI-needing builds) require a
		// logged-in user.
		if autoLogin, alOK := util.TrySh("defaults", []string{"read", "/Library/Preferences/com.apple.loginwindow", "autoLoginUser"}); alOK && autoLogin != "" {
			checks = append(checks, ok("Automatic login enabled ("+autoLogin+")"))
		} else {
			checks = append(checks, warn("Automatic login is OFF — after a reboot the runner won't start until someone logs in. Enable: System Settings → Users & Groups → Automatic login. (If FileVault is on, macOS disables auto-login; consider turning FileVault off on a dedicated CI Mac.)"))
		}
	} else {
		// Linux: services are systemd user units.
		if sysd, sysdOK := util.TrySh("systemctl", []string{"--user", "is-system-running"}); sysdOK && sysd != "" {
			checks = append(checks, ok("systemd user session available ("+sysd+")"))
		} else {
			checks = append(checks, bad("No systemd user session — local-worker needs systemd on Linux (WSL2: enable systemd in /etc/wsl.conf)"))
		}
		// Lingering keeps user services alive without an active login session.
		username := os.Getenv("USER")
		linger, _ := util.TrySh("loginctl", []string{"show-user", username, "--property=Linger"})
		if linger == "Linger=yes" {
			checks = append(checks, ok("Lingering enabled — services survive logout/reboot"))
		} else {
			display := username
			if display == "" {
				display = "$USER"
			}
			checks = append(checks, warn("Lingering is OFF — after a reboot the runner won't start until you log in. Fix: loginctl enable-linger "+display))
		}
		sleepMasked, _ := util.TrySh("systemctl", []string{"is-enabled", "sleep.target"})
		if sleepMasked != "" && sleepMasked != "masked" {
			checks = append(checks, warn("System can suspend — on an always-on box: sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target"))
		} else if sleepMasked == "masked" {
			checks = append(checks, ok("Suspend targets masked"))
		}
	}

	// Optional toolchains
	if docker, dockerOK := util.TrySh("docker", []string{"--version"}); dockerOK {
		if _, daemonOK := util.TrySh("docker", []string{"info", "--format", "ok"}); daemonOK {
			checks = append(checks, ok("Docker CLI + daemon running ("+strings.Split(docker, ",")[0]+")"))
		} else {
			checks = append(checks, warn("Docker CLI present but daemon not running — start OrbStack/Colima/Docker Desktop and set it to launch at login"))
		}
	} else if mac {
		checks = append(checks, warn("Docker not installed — needed only for image builds (brew install orbstack — or colima + docker)"))
	} else {
		checks = append(checks, warn("Docker not installed — needed only for image builds (curl -fsSL https://get.docker.com | sh)"))
	}

	if mac {
		if xcodePath, xcOK := util.TrySh("xcode-select", []string{"-p"}); xcOK && strings.Contains(xcodePath, "Xcode.app") {
			verOut, _ := util.TrySh("xcodebuild", []string{"-version"})
			ver := strings.Split(verOut, "\n")[0]
			if ver == "" {
				ver = "Xcode"
			}
			checks = append(checks, ok(ver+" installed"))
		} else {
			checks = append(checks, warn("Full Xcode not selected — needed only for iOS builds (install from App Store, then: sudo xcode-select -s /Applications/Xcode.app)"))
		}
	}

	if !forSetup {
		// Services
		svc := service.Get()
		for _, r := range cfg.Runners {
			if svc.IsLoaded(util.RunnerLabel(r.Repo)) {
				checks = append(checks, ok("Runner service loaded: "+r.Repo))
			} else {
				checks = append(checks, bad("Runner service NOT loaded: "+r.Repo+" — re-run: lw add "+r.Repo))
			}
		}
		if cfg.Watchdog.Installed {
			if svc.IsLoaded(util.WatchdogLabel) {
				checks = append(checks, ok("Watchdog service loaded"))
			} else {
				checks = append(checks, bad("Watchdog installed but not loaded — run: lw watchdog install"))
			}
		} else {
			checks = append(checks, warn("Watchdog not installed — `lw watchdog install` adds self-healing + offline alerts"))
		}
	}

	return checks
}

func diskFreeGb() (int, bool) {
	out, dfOK := util.TrySh("df", []string{"-g", "/System/Volumes/Data"})
	if !dfOK {
		out, dfOK = util.TrySh("df", []string{"-g", "/"})
	}
	if !dfOK || out == "" {
		return 0, false
	}
	lines := strings.Split(out, "\n")
	cols := strings.Fields(lines[len(lines)-1])
	if len(cols) < 4 {
		return 0, false
	}
	avail, err := strconv.Atoi(cols[3])
	if err != nil {
		return 0, false
	}
	return avail, true
}

func printCheck(c check) {
	switch c.level {
	case levelOK:
		ui.Success(c.msg)
	case levelWarn:
		ui.Warn(c.msg)
	default:
		ui.Error(c.msg)
	}
}

// Doctor diagnoses machine readiness.
func Doctor(flags *Flags) error {
	ui.Intro(" local-worker · doctor ")
	checks := CollectChecks(false)
	bads, warns := 0, 0
	for _, c := range checks {
		printCheck(c)
		switch c.level {
		case levelBad:
			bads++
		case levelWarn:
			warns++
		}
	}
	switch {
	case bads > 0:
		ui.Outro(ui.Red(fmt.Sprintf("%d blocking issue(s), %d warning(s).", bads, warns)))
		os.Exit(1)
	case warns > 0:
		ui.Outro(ui.Yellow(fmt.Sprintf("Ready with %d warning(s).", warns)))
	default:
		ui.Outro(ui.Green("This machine is fully ready to take jobs."))
	}
	return nil
}
