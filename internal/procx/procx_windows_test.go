//go:build windows

package procx

import "golang.org/x/sys/windows"

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	res, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return false
	}
	return res == uint32(windows.WAIT_TIMEOUT)
}
