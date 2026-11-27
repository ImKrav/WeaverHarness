package verify

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/procx"
	"github.com/ImKrav/weaverharness/internal/receipt"
)

const maxOutputTailBytes = 2000

// CheckResult records the re-execution result of a task or receipt command.
type CheckResult struct {
	Cmd        string `json:"cmd"`
	Source     string `json:"source"`
	ExitCode   int    `json:"exit_code"`
	Claimed    *int   `json:"claimed,omitempty"`
	Passed     bool   `json:"passed"`
	TimedOut   bool   `json:"timed_out"`
	OutputTail string `json:"output_tail"`
}

// Verification holds the outcome of verifying a worker receipt against git and shell checks.
type Verification struct {
	CommitsMissing []string         `json:"commits_missing"`
	OnlyInReceipt  []string         `json:"only_in_receipt"`
	OnlyInDiff     []string         `json:"only_in_diff"`
	Checks         []CheckResult    `json:"checks"`
	RefViolations  []gitx.Violation `json:"ref_violations"`
	Passed         bool             `json:"passed"`
}

// Input defines the inputs needed to verify a worker receipt.
type Input struct {
	Worktree   gitx.Repo
	Base       string
	Branch     string
	Receipt    receipt.Receipt
	TaskChecks []string
	Timeout    time.Duration
}

// Verify checks commit ancestry, file diff discrepancies, and re-runs task and receipt commands.
func Verify(ctx context.Context, in Input) Verification {
	v := Verification{
		CommitsMissing: []string{},
		OnlyInReceipt:  []string{},
		OnlyInDiff:     []string{},
		Checks:         []CheckResult{},
		RefViolations:  []gitx.Violation{},
	}

	branch := in.Branch
	if branch == "" {
		branch = "HEAD"
	}

	for _, sha := range in.Receipt.Commits {
		ok, err := in.Worktree.IsAncestor(ctx, sha, branch)
		if err != nil || !ok {
			v.CommitsMissing = append(v.CommitsMissing, sha)
		}
	}

	compareChanges(ctx, in, branch, &v)

	seenTask := make(map[string]bool, len(in.TaskChecks))
	for _, cmd := range in.TaskChecks {
		if seenTask[cmd] {
			continue
		}
		seenTask[cmd] = true
		exit, tail, timedOut := RunCheck(ctx, in.Worktree.Dir, cmd, in.Timeout)
		v.Checks = append(v.Checks, CheckResult{
			Cmd:        cmd,
			Source:     "task",
			ExitCode:   exit,
			Passed:     !timedOut && exit == 0,
			TimedOut:   timedOut,
			OutputTail: tail,
		})
	}

	for _, rc := range in.Receipt.Verification {
		if seenTask[rc.Cmd] {
			continue
		}
		exit, tail, timedOut := RunCheck(ctx, in.Worktree.Dir, rc.Cmd, in.Timeout)
		claimed := rc.ExitCode
		v.Checks = append(v.Checks, CheckResult{
			Cmd:        rc.Cmd,
			Source:     "receipt",
			ExitCode:   exit,
			Claimed:    &claimed,
			Passed:     !timedOut && exit == claimed,
			TimedOut:   timedOut,
			OutputTail: tail,
		})
	}

	v.Finalize()
	return v
}

func compareChanges(ctx context.Context, in Input, branch string, v *Verification) {
	if in.Base == "" {
		return
	}
	diffPaths, err := in.Worktree.DiffNames(ctx, in.Base, branch)
	if err != nil {
		return
	}

	diffSet := make(map[string]bool, len(diffPaths))
	for _, p := range diffPaths {
		diffSet[p] = true
	}

	receiptSet := make(map[string]bool, len(in.Receipt.Changes))
	for _, ch := range in.Receipt.Changes {
		p := normalizeReceiptPath(ch.Path)
		if p == "" || receiptSet[p] {
			continue
		}
		receiptSet[p] = true
		if !diffSet[p] {
			v.OnlyInReceipt = append(v.OnlyInReceipt, p)
		}
	}

	for _, p := range diffPaths {
		if !receiptSet[p] {
			v.OnlyInDiff = append(v.OnlyInDiff, p)
		}
	}
}

func normalizeReceiptPath(raw string) string {
	p := strings.ReplaceAll(strings.TrimSpace(raw), `\`, `/`)
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	if p == "." {
		return ""
	}
	return p
}

// Finalize updates Passed based on missing commits, check results, and ref violations.
func (v *Verification) Finalize() {
	if len(v.CommitsMissing) > 0 || len(v.RefViolations) > 0 {
		v.Passed = false
		return
	}
	for _, c := range v.Checks {
		if !c.Passed {
			v.Passed = false
			return
		}
	}
	v.Passed = true
}

// RunCheck executes cmd inside dir with the platform shell, enforcing timeout via procx.
func RunCheck(ctx context.Context, dir, cmd string, timeout time.Duration) (exit int, tail string, timedOut bool) {
	c := shellCommand(dir, cmd)
	buf := &tailBuffer{limit: maxOutputTailBytes}
	c.Stdout = buf
	c.Stderr = buf

	proc, err := procx.Start(c)
	if err != nil {
		return -1, err.Error(), false
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- proc.Wait()
	}()

	var timerCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerCh = timer.C
	}

	select {
	case err := <-waitCh:
		return exitCode(err), buf.String(), false
	case <-timerCh:
		_ = proc.Kill()
		err := <-waitCh
		code := exitCode(err)
		if code == 0 {
			code = -1
		}
		return code, buf.String(), true
	case <-ctx.Done():
		_ = proc.Kill()
		err := <-waitCh
		code := exitCode(err)
		if code == 0 {
			code = -1
		}
		return code, buf.String(), false
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	if n >= b.limit {
		b.data = append(b.data[:0], p[n-b.limit:]...)
		return n, nil
	}
	if len(b.data)+n > b.limit {
		drop := len(b.data) + n - b.limit
		copy(b.data, b.data[drop:])
		b.data = b.data[:len(b.data)-drop]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
