// Linux service backend: systemd user units.
//
// Runners get a plain service with Restart=always; the watchdog gets a
// oneshot service driven by a timer. User units stop at logout unless
// lingering is enabled — `lw doctor` checks for that.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

type linuxBackend struct{}

func unitDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func unitPath(name string) string {
	return filepath.Join(unitDir(), name)
}

func systemctl(args ...string) (string, int) {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	return string(out), code
}

func unitQuote(a string) string {
	return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
}

func BuildService(spec Spec) string {
	var envLines strings.Builder
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envLines, "Environment=%s\n", unitQuote(k+"="+spec.Env[k]))
	}
	execStart := make([]string, len(spec.ProgramArgs))
	for i, a := range spec.ProgramArgs {
		execStart[i] = unitQuote(a)
	}
	mode := "Type=oneshot\n"
	if spec.KeepAlive {
		mode = "Restart=always\nRestartSec=5\n"
	}
	workingDir := ""
	if spec.WorkingDir != "" {
		workingDir = "WorkingDirectory=" + spec.WorkingDir + "\n"
	}
	return fmt.Sprintf(`[Unit]
Description=local-worker %s
After=network-online.target

[Service]
%sExecStart=%s
%sStandardOutput=append:%s
StandardError=append:%s
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/home/linuxbrew/.linuxbrew/bin
%s
[Install]
WantedBy=default.target
`, spec.Label, mode, strings.Join(execStart, " "), workingDir, spec.LogPath, spec.LogPath, envLines.String())
}

func BuildTimer(label string, intervalSeconds int) string {
	return fmt.Sprintf(`[Unit]
Description=local-worker timer for %s

[Timer]
OnBootSec=60
OnUnitActiveSec=%d

[Install]
WantedBy=timers.target
`, label, intervalSeconds)
}

func (linuxBackend) Install(spec Spec) error {
	if err := os.MkdirAll(unitDir(), 0o755); err != nil {
		return err
	}
	serviceName := spec.Label + ".service"
	serviceSpec := spec
	serviceSpec.KeepAlive = spec.KeepAlive && spec.StartInterval == 0
	if err := os.WriteFile(unitPath(serviceName), []byte(BuildService(serviceSpec)), 0o600); err != nil {
		return err
	}
	target := serviceName
	if spec.StartInterval > 0 {
		target = spec.Label + ".timer"
		if err := os.WriteFile(unitPath(target), []byte(BuildTimer(spec.Label, spec.StartInterval)), 0o600); err != nil {
			return err
		}
	}
	systemctl("daemon-reload")
	if out, code := systemctl("enable", "--now", target); code != 0 {
		username := ""
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		return fmt.Errorf(
			"systemctl --user enable --now %s failed:\n%s\nIs a systemd user session available? (On servers/SSH: loginctl enable-linger %s)",
			target, out, username)
	}
	return nil
}

func (linuxBackend) IsLoaded(label string) bool {
	_, svcCode := systemctl("is-enabled", label+".service")
	_, timerCode := systemctl("is-enabled", label+".timer")
	return svcCode == 0 || timerCode == 0
}

func (linuxBackend) Uninstall(label string) {
	for _, name := range []string{label + ".timer", label + ".service"} {
		systemctl("disable", "--now", name)
		os.Remove(unitPath(name))
	}
	systemctl("daemon-reload")
}

func (linuxBackend) Kickstart(label string) bool {
	_, code := systemctl("restart", label+".service")
	return code == 0
}
