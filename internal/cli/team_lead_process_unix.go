//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

func signalExternalLeadWakeProcessGroup(pgid int, signal syscall.Signal) error {
	return syscall.Kill(-pgid, signal)
}

func ownExternalLeadWakeProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
