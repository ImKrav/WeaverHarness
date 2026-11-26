//go:build !windows

package procx

import (
	"errors"
	"syscall"
)

type osState struct {
	pgid int
}

func (p *Proc) startOS() error {
	if p.Cmd.SysProcAttr == nil {
		p.Cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	p.Cmd.SysProcAttr.Setpgid = true

	if err := p.Cmd.Start(); err != nil {
		return err
	}
	p.os.pgid = p.Cmd.Process.Pid
	return nil
}

func (p *Proc) killOS() error {
	if p.os.pgid <= 0 {
		return nil
	}
	err := syscall.Kill(-p.os.pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *Proc) closeOS() {}
