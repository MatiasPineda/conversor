//go:build !windows

package engines

import (
	"os/exec"
	"syscall"
)

// prepareCommand starts the tool in its own process group, so cancelling
// can stop everything it spawned (LibreOffice's soffice.bin, for one).
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills the tool's process group.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	cmd.Process.Kill()
}
