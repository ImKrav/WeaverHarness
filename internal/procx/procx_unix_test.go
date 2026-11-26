//go:build !windows

package procx

import "syscall"

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
