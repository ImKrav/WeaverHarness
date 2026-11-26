package procx

import (
	"os/exec"
	"sync"
)

// Proc wraps an exec.Cmd so its entire process tree can be terminated reliably.
type Proc struct {
	Cmd    *exec.Cmd
	mu     sync.Mutex
	killed bool
	waited bool
	os     osState
}

// Start launches cmd inside a process group (Unix) or Job Object (Windows).
func Start(cmd *exec.Cmd) (*Proc, error) {
	p := &Proc{Cmd: cmd}
	if err := p.startOS(); err != nil {
		return nil, err
	}
	return p, nil
}

// Kill terminates the process and all descendants. It is safe to call repeatedly.
func (p *Proc) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.killed || p.waited {
		return nil
	}
	err := p.killOS()
	p.killed = true
	return err
}

// Wait waits for the root command to exit and releases OS tree resources.
func (p *Proc) Wait() error {
	err := p.Cmd.Wait()

	p.mu.Lock()
	p.waited = true
	p.closeOS()
	p.mu.Unlock()

	return err
}
