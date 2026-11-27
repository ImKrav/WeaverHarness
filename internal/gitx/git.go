package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotGit is returned when a directory is not inside a git working tree.
var ErrNotGit = errors.New("no es un repositorio git")

// WeaverRefPrefix is the branch namespace managed by WeaverHarness.
const WeaverRefPrefix = "refs/heads/weaver/"

// Repo represents a git repository rooted at Dir.
type Repo struct {
	Dir string
}

// WorktreeInfo describes one git worktree entry reported by git worktree list.
type WorktreeInfo struct {
	Path   string
	Branch string // nombre corto sin "refs/heads/", vacÃ­o si detached
	Head   string
}

// Open resolves the git repository top-level directory containing dir.
func Open(dir string) (Repo, error) {
	probe := Repo{Dir: dir}
	top, err := probe.Run(context.Background(), "rev-parse", "--show-toplevel")
	if err != nil {
		return Repo{}, fmt.Errorf("%w: %v", ErrNotGit, err)
	}
	return Repo{Dir: filepath.Clean(filepath.FromSlash(top))}, nil
}

// Run executes git -C r.Dir with args and returns trimmed stdout.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"-C", r.Dir}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, errMsg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CurrentBranch returns the current branch name, or empty string if HEAD is detached.
func (r Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.Run(ctx, "branch", "--show-current")
}

// IsDirty reports whether the working tree has staged, unstaged, or untracked changes.
func (r Repo) IsDirty(ctx context.Context) (bool, error) {
	out, err := r.Run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// CommonDir returns the absolute path to the shared git directory.
func (r Repo) CommonDir(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := filepath.FromSlash(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir, p)
	}
	return filepath.Clean(p), nil
}

// RevParse resolves ref to its full object SHA.
func (r Repo) RevParse(ctx context.Context, ref string) (string, error) {
	return r.Run(ctx, "rev-parse", "--verify", ref)
}

// AddWorktree creates a new git worktree at path on a new branch rooted at base, pruning stale registrations once on failure.
func (r Repo) AddWorktree(ctx context.Context, path, newBranch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("gitx: mkdir worktree parent: %w", err)
	}
	_, branchBeforeErr := r.RevParse(ctx, "refs/heads/"+newBranch)
	_, err := r.Run(ctx, "worktree", "add", "-b", newBranch, path, base)
	if err != nil {
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			_, _ = r.Run(ctx, "worktree", "prune")
			if branchBeforeErr != nil {
				_ = r.DeleteBranch(ctx, newBranch)
			}
			_, err = r.Run(ctx, "worktree", "add", "-b", newBranch, path, base)
		}
	}
	return err
}

// AttachWorktree attaches a git worktree at path to an existing branch, pruning stale worktree entries first.
func (r Repo) AttachWorktree(ctx context.Context, path, existingBranch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("gitx: mkdir worktree parent: %w", err)
	}
	_, _ = r.Run(ctx, "worktree", "prune")
	_, err := r.Run(ctx, "worktree", "add", path, existingBranch)
	return err
}

// RemoveWorktree forcefully removes the git worktree at path.
func (r Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.Run(ctx, "worktree", "remove", "--force", path)
	return err
}

// PruneWorktrees removes stale git worktree administrative files.
func (r Repo) PruneWorktrees(ctx context.Context) error {
	_, err := r.Run(ctx, "worktree", "prune")
	return err
}

// ListWorktrees returns all registered git worktrees for this repository.
func (r Repo) ListWorktrees(ctx context.Context) ([]WorktreeInfo, error) {
	out, err := r.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var list []WorktreeInfo
	var cur *WorktreeInfo
	flush := func() {
		if cur != nil && cur.Path != "" {
			list = append(list, *cur)
		}
		cur = nil
	}

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			p := strings.TrimPrefix(line, "worktree ")
			cur = &WorktreeInfo{Path: filepath.Clean(filepath.FromSlash(p))}
		case cur != nil && strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case cur != nil && strings.HasPrefix(line, "branch "):
			br := strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(br, "refs/heads/")
		}
	}
	flush()
	return list, nil
}

// ListWeaverBranches returns short branch names matching "weaver/...".
func (r Repo) ListWeaverBranches(ctx context.Context) ([]string, error) {
	out, err := r.Run(ctx, "for-each-ref", "--format=%(refname:short)", WeaverRefPrefix)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	branches := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// DeleteBranch forcefully deletes branch name (-D).
func (r Repo) DeleteBranch(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "branch", "-D", name)
	return err
}

// SwitchNew creates and checks out branch at base in this repository/worktree.
func (r Repo) SwitchNew(ctx context.Context, branch, base string) error {
	_, err := r.Run(ctx, "switch", "-c", branch, base)
	return err
}

// CommitAll stages all changes and commits them if the tree is dirty.
func (r Repo) CommitAll(ctx context.Context, msg string) (bool, error) {
	if _, err := r.Run(ctx, "add", "-A"); err != nil {
		return false, err
	}
	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	if _, err := r.Run(ctx, "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// IsAncestor reports whether commit a is an ancestor of commit b.
func (r Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "merge-base", "--is-ancestor", a, b)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	errMsg := strings.TrimSpace(stderr.String())
	if errMsg == "" {
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", a, b, err)
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s", a, b, err, errMsg)
}

// DiffNames lists changed file paths between base and head using forward slashes.
func (r Repo) DiffNames(ctx context.Context, base, head string) ([]string, error) {
	out, err := r.Run(ctx, "-c", "core.quotepath=false", "diff", "--name-only", base+".."+head)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []string{}, nil
	}
	lines := strings.Split(out, "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(line))
	}
	return paths, nil
}

// DiffStat returns a color-free git diff --stat summary between base and head.
func (r Repo) DiffStat(ctx context.Context, base, head string) (string, error) {
	return r.Run(ctx, "-c", "core.quotepath=false", "diff", "--stat", "--no-color", "--no-ext-diff", base+".."+head)
}

// Diff returns a color-free git patch between base and head, optionally filtered to path.
func (r Repo) Diff(ctx context.Context, base, head, path string) (string, error) {
	args := []string{"-c", "core.quotepath=false", "diff", "--no-color", "--no-ext-diff", base + ".." + head}
	if path != "" {
		args = append(args, "--", filepath.ToSlash(path))
	}
	return r.Run(ctx, args...)
}

// MergeFFOnly fast-forwards the current branch to ref.
func (r Repo) MergeFFOnly(ctx context.Context, ref string) error {
	_, err := r.Run(ctx, "merge", "--ff-only", ref)
	return err
}

// WeaverDir returns the sibling directory used for WeaverHarness worktrees.
func WeaverDir(repoTop string) string {
	clean := filepath.Clean(filepath.FromSlash(repoTop))
	return filepath.Join(filepath.Dir(clean), filepath.Base(clean)+"-weaver")
}
