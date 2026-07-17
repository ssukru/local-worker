// Package service abstracts the per-platform background service manager
// (launchd LaunchAgents on macOS, systemd user units on Linux) — the Go port
// of src/service/.
package service

import (
	"errors"
	"runtime"
)

// Spec describes a background service to install.
type Spec struct {
	Label         string
	ProgramArgs   []string
	WorkingDir    string
	LogPath       string
	KeepAlive     bool
	StartInterval int               // seconds; periodic instead of keep-alive
	Env           map[string]string // may carry GH_TOKEN — unit files are chmod 600
}

type Backend interface {
	Install(spec Spec) error
	Uninstall(label string)
	IsLoaded(label string) bool
	Kickstart(label string) bool
}

// Get returns the platform backend, or nil on unsupported platforms (Windows).
func Get() Backend {
	switch runtime.GOOS {
	case "darwin":
		return darwinBackend{}
	case "linux":
		return linuxBackend{}
	default:
		return nil
	}
}

func AssertPlatform() error {
	if Get() != nil {
		return nil
	}
	return errors.New(
		"local-worker runs natively on macOS and Linux.\n" +
			"On Windows, use WSL2: install a distro (wsl --install -d Ubuntu), then\n" +
			"install local-worker inside WSL and run it there. Jobs will run\n" +
			"in Linux — which is what GitHub's ubuntu-* runners are anyway.")
}
