//go:build !windows

package process

import (
	"os"
	"os/exec"
	"syscall"
)

type defaultController struct{}

func (defaultController) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (defaultController) Terminate(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if proc, _ := os.FindProcess(pid); proc != nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
}

func (defaultController) Kill(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if proc, _ := os.FindProcess(pid); proc != nil {
		_ = proc.Kill()
	}
}

func (defaultController) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	return err == nil && proc.Signal(syscall.Signal(0)) == nil
}
