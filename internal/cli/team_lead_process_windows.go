package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func signalExternalLeadWakeProcessGroup(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if signal == 0 {
		return process.Signal(syscall.Signal(0))
	}
	return process.Kill()
}

func ownExternalLeadWakeProcessGroup(cmd *exec.Cmd) {
	// Windows wake is unsupported. Direct team launches are owned by a Job
	// Object in the Windows terminal backend instead of a Unix process group.
}
