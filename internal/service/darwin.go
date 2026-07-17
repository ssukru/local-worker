// macOS service backend: LaunchAgents in the user's GUI session.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type darwinBackend struct{}

func agentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func plistPath(label string) string {
	return filepath.Join(agentsDir(), label+".plist")
}

func launchctl(args ...string) (string, int) {
	cmd := exec.Command("launchctl", args...)
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

func guiDomain(label string) string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func BuildPlist(spec Spec) string {
	var envEntries strings.Builder
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envEntries, "      <key>%s</key>\n      <string>%s</string>\n", xmlEsc(k), xmlEsc(spec.Env[k]))
	}
	if envEntries.Len() == 0 {
		envEntries.WriteString("\n")
	}
	var args strings.Builder
	for _, a := range spec.ProgramArgs {
		fmt.Fprintf(&args, "      <string>%s</string>\n", xmlEsc(a))
	}
	var extras []string
	if spec.KeepAlive {
		extras = append(extras, "    <key>KeepAlive</key>", "    <true/>")
		// GUI session + Interactive keep Xcode/simulator builds working.
		extras = append(extras, "    <key>ProcessType</key>", "    <string>Interactive</string>")
		extras = append(extras, "    <key>SessionCreate</key>", "    <true/>")
	}
	if spec.StartInterval > 0 {
		extras = append(extras, "    <key>StartInterval</key>", fmt.Sprintf("    <integer>%d</integer>", spec.StartInterval))
	}
	if spec.WorkingDir != "" {
		extras = append(extras, "    <key>WorkingDirectory</key>", fmt.Sprintf("    <string>%s</string>", xmlEsc(spec.WorkingDir)))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
%s    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key>
      <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
      <key>LANG</key>
      <string>en_US.UTF-8</string>
%s    </dict>
%s
  </dict>
</plist>
`, xmlEsc(spec.Label), args.String(), xmlEsc(spec.LogPath), xmlEsc(spec.LogPath), envEntries.String(), strings.Join(extras, "\n"))
}

func (darwinBackend) IsLoaded(label string) bool {
	_, code := launchctl("print", guiDomain(label))
	return code == 0
}

func (d darwinBackend) Install(spec Spec) error {
	if err := os.MkdirAll(agentsDir(), 0o755); err != nil {
		return err
	}
	file := plistPath(spec.Label)
	d.unload(spec.Label, file)
	if err := os.WriteFile(file, []byte(BuildPlist(spec)), 0o600); err != nil {
		return err
	}
	out, code := launchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), file)
	if code != 0 {
		legacyOut, legacyCode := launchctl("load", "-w", file)
		if legacyCode != 0 {
			return fmt.Errorf("launchctl failed to load %s:\n%s%s", file, out, legacyOut)
		}
	}
	return nil
}

func (d darwinBackend) unload(label, file string) {
	if d.IsLoaded(label) {
		if _, code := launchctl("bootout", guiDomain(label)); code != 0 {
			launchctl("unload", "-w", file)
		}
	}
}

func (d darwinBackend) Uninstall(label string) {
	file := plistPath(label)
	d.unload(label, file)
	os.Remove(file)
}

func (darwinBackend) Kickstart(label string) bool {
	_, code := launchctl("kickstart", "-k", guiDomain(label))
	return code == 0
}
