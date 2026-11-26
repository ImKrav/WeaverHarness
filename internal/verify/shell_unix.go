//go:build !windows

package verify

import "os/exec"

func shellCommand(dir, cmd string) *exec.Cmd {
	c := exec.Command("sh", "-c", cmd)
	c.Dir = dir
	return c
}
