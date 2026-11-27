package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/run"
)

// CleanOptions configures app.Clean.
type CleanOptions struct {
	Dir    string
	DryRun bool
	All    bool // incluye runs no integrados (open/spec_approved/plan_approved)
	Force  bool // omite confirmación [s/N] cuando All == true
	In     io.Reader
	Out    io.Writer
}

// Clean safely removes finished or orphaned weaver worktrees, branches, and run states.
func Clean(ctx context.Context, o CleanOptions) error {
	dir := o.Dir
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("clean: leyendo directorio actual: %w", err)
		}
	}
	in := o.In
	if in == nil {
		in = os.Stdin
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}

	repo, err := gitx.Open(dir)
	if err != nil {
		return err
	}

	wts, err := repo.ListWorktrees(ctx)
	if err != nil {
		return fmt.Errorf("clean: listando worktrees: %w", err)
	}

	mainRepoDir := repo.Dir
	var mainWTBranch string
	if len(wts) > 0 {
		mainRepoDir = wts[0].Path
		mainWTBranch = wts[0].Branch
	}
	weaverDir := gitx.WeaverDir(mainRepoDir)

	if pathWithin(dir, weaverDir) || pathWithin(repo.Dir, weaverDir) {
		return fmt.Errorf("clean: no puedes ejecutar 'weaver clean' desde dentro de un worktree de weaver (%s); ejecútalo desde el repositorio principal (%s)", dir, mainRepoDir)
	}

	store := run.Store{Root: filepath.Join(mainRepoDir, ".weaver", "runs")}
	runs, err := store.List()
	if err != nil {
		return fmt.Errorf("clean: listando ejecuciones: %w", err)
	}

	var openRuns, targetRuns []*run.State
	for _, st := range runs {
		if st.Phase != run.PhaseIntegrated {
			openRuns = append(openRuns, st)
			if o.All {
				targetRuns = append(targetRuns, st)
			}
		} else {
			targetRuns = append(targetRuns, st)
		}
	}

	protectedDirs := make(map[string]bool)
	protectedBranches := make(map[string]bool)
	if !o.All {
		for _, st := range openRuns {
			if st.SessionDir != "" {
				protectedDirs[normPath(st.SessionDir)] = true
			}
			if st.IntegrationBranch != "" {
				protectedBranches[st.IntegrationBranch] = true
			}
			for _, ag := range st.Agents {
				if ag.Worktree != "" {
					protectedDirs[normPath(ag.Worktree)] = true
				}
				if ag.Branch != "" {
					protectedBranches[ag.Branch] = true
				}
			}
		}
	}

	mainNorm := normPath(mainRepoDir)
	var targetWorktrees []gitx.WorktreeInfo
	targetWTSet := make(map[string]bool)
	for _, wt := range wts {
		wtNorm := normPath(wt.Path)
		if wtNorm == mainNorm {
			continue
		}
		if !o.All && protectedDirs[wtNorm] {
			continue
		}
		if pathWithin(wt.Path, weaverDir) {
			targetWorktrees = append(targetWorktrees, wt)
			targetWTSet[wtNorm] = true
		}
	}

	weaverBranches, err := repo.ListWeaverBranches(ctx)
	if err != nil {
		return fmt.Errorf("clean: listando ramas weaver: %w", err)
	}
	var targetBranches []string
	targetBranchSet := make(map[string]bool)
	for _, br := range weaverBranches {
		if br == mainWTBranch {
			continue
		}
		if !o.All && protectedBranches[br] {
			continue
		}
		targetBranches = append(targetBranches, br)
		targetBranchSet[br] = true
	}

	if len(targetWorktrees) == 0 && len(targetBranches) == 0 && len(targetRuns) == 0 {
		_, _ = fmt.Fprintln(out, "No hay nada que limpiar.")
		return nil
	}

	if o.DryRun {
		_, _ = fmt.Fprintln(out, "Plan de limpieza (--dry-run):")
		for _, wt := range targetWorktrees {
			_, _ = fmt.Fprintf(out, "  - Worktree: %s\n", wt.Path)
		}
		for _, br := range targetBranches {
			_, _ = fmt.Fprintf(out, "  - Rama: %s\n", br)
		}
		for _, st := range targetRuns {
			_, _ = fmt.Fprintf(out, "  - Run: %s (%s)\n", st.RunID, st.Phase)
		}
		return nil
	}

	if o.All && !o.Force {
		_, _ = fmt.Fprintf(out, "¿Eliminar también %d ejecuciones abiertas y todos los worktrees de weaver? [s/N] ", len(openRuns))
		line, _ := bufio.NewReader(in).ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans != "s" && ans != "si" && ans != "sí" && ans != "y" && ans != "yes" {
			_, _ = fmt.Fprintln(out, "Limpieza cancelada.")
			return nil
		}
	}

	for _, wt := range targetWorktrees {
		if err := repo.RemoveWorktree(ctx, wt.Path); err != nil {
			if _, statErr := os.Stat(wt.Path); statErr == nil {
				return fmt.Errorf("clean: eliminando worktree %s: %w", wt.Path, err)
			}
		}
		_, _ = fmt.Fprintf(out, "Worktree eliminado: %s\n", wt.Path)
	}

	if err := repo.PruneWorktrees(ctx); err != nil {
		return fmt.Errorf("clean: podando worktrees: %w", err)
	}

	// Si algún worktree preservado (p.ej. SessionDir compartido) sigue en una rama a eliminar,
	// pasamos HEAD a detached antes de git branch -D.
	for _, wt := range wts {
		wtNorm := normPath(wt.Path)
		if wtNorm == mainNorm || targetWTSet[wtNorm] || wt.Branch == "" || !targetBranchSet[wt.Branch] {
			continue
		}
		if _, err := os.Stat(wt.Path); err == nil {
			if _, swErr := (gitx.Repo{Dir: wt.Path}).Run(ctx, "switch", "--detach"); swErr != nil {
				return fmt.Errorf("clean: liberando rama %s en %s: %w", wt.Branch, wt.Path, swErr)
			}
		}
	}

	for _, br := range targetBranches {
		if err := repo.DeleteBranch(ctx, br); err != nil {
			return fmt.Errorf("clean: eliminando rama %s: %w", br, err)
		}
		_, _ = fmt.Fprintf(out, "Rama eliminada: %s\n", br)
	}

	for _, st := range targetRuns {
		if err := store.Delete(st.RunID); err != nil {
			return fmt.Errorf("clean: eliminando run %s: %w", st.RunID, err)
		}
		_, _ = fmt.Fprintf(out, "Run eliminado: %s\n", st.RunID)
	}

	if entries, err := os.ReadDir(weaverDir); err == nil && len(entries) == 0 {
		_ = os.Remove(weaverDir)
	}

	return nil
}

func normPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func pathWithin(child, parent string) bool {
	c := normPath(child)
	p := normPath(parent)
	if c == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
