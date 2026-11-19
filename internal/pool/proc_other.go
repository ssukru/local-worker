//go:build !unix

package pool

import "os/exec"

// local-worker refuses to run on non-unix platforms (see
// service.AssertPlatform); these keep the package compiling there.

func isolateProcess(cmd *exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func killGroup(cmd *exec.Cmd) { terminateGroup(cmd) }
