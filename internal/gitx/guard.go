package gitx

import (
	"cmp"
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// Snapshot records repository refs and worktree HEAD commits at a point in time.
type Snapshot struct {
	Refs  map[string]string
	Heads map[string]string
}

// Violation describes an unexpected ref or worktree HEAD mutation detected by Guard.
type Violation struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Restored bool   `json:"restored"`
}

// Guard monitors non-weaver git refs and worktree HEADs across subagent runs.
type Guard struct {
	repo Repo
	base Snapshot
	mu   sync.Mutex
}

// NewGuard captures an initial ref and worktree HEAD snapshot for repo r.
func NewGuard(ctx context.Context, r Repo) (*Guard, error) {
	snap, err := takeSnapshot(ctx, r)
	if err != nil {
		return nil, err
	}
	return &Guard{
		repo: r,
		base: snap,
	}, nil
}

// Rebase updates the guard's baseline snapshot after intentional weaver changes.
func (g *Guard) Rebase(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap, err := takeSnapshot(ctx, g.repo)
	if err != nil {
		return err
	}
	g.base = snap
	return nil
}

// Check compares current refs and worktree HEADs against the baseline, restores
// protected local branches/tags, and advances the baseline snapshot.
func (g *Guard) Check(ctx context.Context) ([]Violation, error) {
	// shortcut: no detecta que un agente mueva otra rama weaver/ ni la de integración; atribuir por agente si hace falta
	g.mu.Lock()
	defer g.mu.Unlock()

	cur, err := takeSnapshot(ctx, g.repo)
	if err != nil {
		return nil, err
	}

	violations := []Violation{}

	for ref, beforeSHA := range g.base.Refs {
		if strings.HasPrefix(ref, WeaverRefPrefix) {
			continue
		}
		afterSHA, exists := cur.Refs[ref]

		if strings.HasPrefix(ref, "refs/remotes/") {
			if !exists || afterSHA != beforeSHA {
				violations = append(violations, Violation{
					Kind:   "remote_changed",
					Ref:    ref,
					Before: beforeSHA,
					After:  afterSHA,
				})
			}
			continue
		}

		isProtected := strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/tags/")
		if !isProtected {
			continue
		}

		if !exists {
			_, restoreErr := g.repo.Run(ctx, "update-ref", ref, beforeSHA)
			violations = append(violations, Violation{
				Kind:     "deleted",
				Ref:      ref,
				Before:   beforeSHA,
				Restored: restoreErr == nil,
			})
			continue
		}

		if afterSHA != beforeSHA {
			_, restoreErr := g.repo.Run(ctx, "update-ref", ref, beforeSHA)
			violations = append(violations, Violation{
				Kind:     "moved",
				Ref:      ref,
				Before:   beforeSHA,
				After:    afterSHA,
				Restored: restoreErr == nil,
			})
		}
	}

	for ref, afterSHA := range cur.Refs {
		if strings.HasPrefix(ref, WeaverRefPrefix) {
			continue
		}
		if _, existed := g.base.Refs[ref]; existed {
			continue
		}
		if strings.HasPrefix(ref, "refs/remotes/") {
			violations = append(violations, Violation{
				Kind:  "remote_changed",
				Ref:   ref,
				After: afterSHA,
			})
			continue
		}
		violations = append(violations, Violation{
			Kind:  "created",
			Ref:   ref,
			After: afterSHA,
		})
	}

	postRestore, err := takeSnapshot(ctx, g.repo)
	if err != nil {
		return nil, err
	}

	weaverRoot := WeaverDir(g.repo.Dir)
	for wt, beforeHead := range g.base.Heads {
		if isUnderDir(wt, weaverRoot) {
			continue
		}
		if afterHead, ok := postRestore.Heads[wt]; ok && afterHead != beforeHead {
			violations = append(violations, Violation{
				Kind:   "head_changed",
				Ref:    wt,
				Before: beforeHead,
				After:  afterHead,
			})
		}
	}

	slices.SortFunc(violations, func(a, b Violation) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.Ref, b.Ref)
	})

	g.base = postRestore
	return violations, nil
}

func takeSnapshot(ctx context.Context, r Repo) (Snapshot, error) {
	snap := Snapshot{
		Refs:  map[string]string{},
		Heads: map[string]string{},
	}

	refsOut, err := r.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return Snapshot{}, err
	}
	for _, line := range strings.Split(refsOut, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			snap.Refs[parts[0]] = parts[1]
		}
	}

	wtOut, err := r.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return Snapshot{}, err
	}
	var currentWT string
	for _, line := range strings.Split(wtOut, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			currentWT = ""
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentWT = normPath(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "HEAD ") && currentWT != "":
			snap.Heads[currentWT] = strings.TrimPrefix(line, "HEAD ")
		}
	}

	return snap, nil
}

func normPath(p string) string {
	clean := filepath.Clean(filepath.FromSlash(p))
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func isUnderDir(path, dir string) bool {
	p := normPath(path)
	d := normPath(dir)
	return p == d || strings.HasPrefix(p, d+string(filepath.Separator))
}
