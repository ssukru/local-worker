//go:build unix

package pool

import (
	"os/exec"
	"syscall"
)

// isolateProcess puts the runner in its own process group. run.sh spawns
// Runner.Listener as a child, so signalling the shell alone would orphan the
// listener and leave a runner registered with GitHub forever.
func isolateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sends sig to the whole process group started by isolateProcess.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative pid targets the group; the pgid equals the leader's pid.
	syscall.Kill(-cmd.Process.Pid, sig)
}

func terminateGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGTERM) }
func killGroup(cmd *exec.Cmd)      { signalGroup(cmd, syscall.SIGKILL) }
