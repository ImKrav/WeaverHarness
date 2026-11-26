//go:build windows

package procx

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type osState struct {
	job windows.Handle
}

func (p *Proc) startOS() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("procx: create job object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("procx: configure job object: %w", err)
	}

	if err := p.Cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}

	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(p.Cmd.Process.Pid),
	)
	if err != nil {
		// Process may have already exited; keep job handle zeroed so Kill falls back cleanly.
		_ = windows.CloseHandle(job)
		return nil
	}
	defer windows.CloseHandle(procHandle)

	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		// If assignment failed (e.g. process already exited), check whether it is still running.
		if isProcessAlive(uint32(p.Cmd.Process.Pid)) {
			_ = p.Cmd.Process.Kill()
			_ = windows.CloseHandle(job)
			return fmt.Errorf("procx: assign process to job object: %w", err)
		}
		_ = windows.CloseHandle(job)
		return nil
	}

	p.os.job = job
	return nil
}

func isProcessAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
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

func (p *Proc) killOS() error {
	if p.os.job != 0 {
		err := windows.TerminateJobObject(p.os.job, 1)
		_ = windows.CloseHandle(p.os.job)
		p.os.job = 0
		if err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("procx: terminate job object: %w", err)
		}
		return nil
	}
	if p.Cmd.Process != nil {
		return p.Cmd.Process.Kill()
	}
	return nil
}

func (p *Proc) closeOS() {
	if p.os.job != 0 {
		_ = windows.CloseHandle(p.os.job)
		p.os.job = 0
	}
}
