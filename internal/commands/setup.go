package commands

import (
	"os"
	"regexp"
	"runtime"

	"github.com/ssukru/local-worker/internal/ui"
	"github.com/ssukru/local-worker/internal/util"
)

var powerIssueRe = regexp.MustCompile(`(?i)sleep|Auto-restart|Linger|suspend`)

// Setup is the guided first-time wizard.
func Setup(flags *Flags) error {
	ui.Intro(" local-worker · setup ")
	ui.Message(
		"This wizard turns this machine into a self-hosted GitHub Actions runner:\n" +
			"  1. check tools & machine settings\n" +
			"  2. sign in to GitHub\n" +
			"  3. attach one or more repositories\n" +
			"  4. install the health watchdog\n" +
			"Everything lives in " + ui.Cyan("~/.local-worker") + "; run it again anytime.")

	if !util.IsTTY() {
		ui.Error("setup is interactive — run it in a terminal. For scripting, use `lw add owner/repo --yes`.")
		os.Exit(1)
	}

	// 1. GitHub CLI presence + auth ------------------------------------------
	if _, ghOK := util.TrySh("gh", []string{"--version"}); !ghOK {
		doInstall, err := ui.Confirm("GitHub CLI (gh) is required and not installed. Install it with Homebrew now?", true)
		if err != nil || !doInstall {
			ui.Cancel("Cannot continue without gh.")
			return nil
		}
		if err := util.Run("brew", "install", "gh"); err != nil {
			return err
		}
	}
	if !util.GhAuthed() {
		ui.Step("Sign in to GitHub — a browser window will guide you (device flow).")
		if err := util.Run("gh", "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web"); err != nil {
			return err
		}
	}
	ui.Success("Signed in as " + ui.Bold(util.GhLogin()))

	// 2. Machine readiness ----------------------------------------------------
	ui.Step("Machine check")
	checks := CollectChecks(true)
	for _, c := range checks {
		printCheck(c)
	}
	powerIssues := false
	hasBad := false
	for _, c := range checks {
		if c.level != levelOK && powerIssueRe.MatchString(c.msg) {
			powerIssues = true
		}
		if c.level == levelBad {
			hasBad = true
		}
	}
	if powerIssues && runtime.GOOS == "darwin" {
		fix, err := ui.Confirm("Apply recommended power settings now? (runs sudo pmset — you'll be asked for your password)", true)
		if err == nil && fix {
			if err := util.Run("sudo", "pmset", "-a", "sleep", "0", "disksleep", "0", "displaysleep", "10", "autorestart", "1", "womp", "1"); err != nil {
				return err
			}
			if err := util.Run("sudo", "pmset", "-a", "disablesleep", "1"); err != nil {
				return err
			}
			ui.Success("Power settings applied — this Mac will stay awake, even with the lid closed.")
		}
	} else if powerIssues && runtime.GOOS == "linux" {
		fix, err := ui.Confirm("Enable lingering + mask suspend targets now? (uses loginctl and sudo systemctl)", true)
		if err == nil && fix {
			if err := util.Run("loginctl", "enable-linger", os.Getenv("USER")); err != nil {
				return err
			}
			if err := util.Run("sudo", "systemctl", "mask", "sleep.target", "suspend.target", "hibernate.target", "hybrid-sleep.target"); err != nil {
				return err
			}
			ui.Success("This machine will stay available for jobs.")
		}
	}
	if hasBad {
		cont, err := ui.Confirm("There are blocking issues above. Continue anyway?", false)
		if err != nil || !cont {
			ui.Cancel("Fix the issues and re-run `lw setup`.")
			return nil
		}
	}

	// 3. Attach repositories --------------------------------------------------
	attached := 0
	for {
		repo, err := PickRepo()
		if err != nil {
			return err
		}
		if repo == "" {
			break
		}
		if err := Add(repo, flags, true); err != nil {
			return err
		}
		attached++
		again, err := ui.Confirm("Attach another repository?", false)
		if err != nil || !again {
			break
		}
	}

	// 4. Watchdog -------------------------------------------------------------
	wantWatchdog, err := ui.Confirm("Install the watchdog? (restarts dead runners, alerts you if this machine goes offline)", true)
	if err == nil && wantWatchdog {
		if err := Watchdog("install", flags); err != nil {
			return err
		}
	}

	// Done --------------------------------------------------------------------
	if attached > 0 {
		ui.Step("How to route jobs to this machine:")
		if err := Integrate("", flags); err != nil {
			return err
		}
	}
	ui.Outro(ui.Green("Setup complete. ") +
		"Useful commands: " + ui.Cyan("lw status") + ", " + ui.Cyan("lw doctor") + ", " + ui.Cyan("lw logs") + ".")
	return nil
}
