//go:build windows

package verify

import (
	"os/exec"
	"syscall"
)

func shellCommand(dir, cmd string) *exec.Cmd {
	c := exec.Command("cmd")
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd /s /c "` + cmd + `"`,
	}
	return c
}
