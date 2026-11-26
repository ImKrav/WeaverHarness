package app

import (
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/run"
)

// Runs prints a summary table of saved runs for the repository at dir.
func Runs(dir string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	repo, err := gitx.Open(dir)
	if err != nil {
		return err
	}
	store := run.Store{Root: filepath.Join(repo.Dir, ".weaver", "runs")}
	list, err := store.List()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No hay ejecuciones guardadas.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tFASE\tRAMA\tTAREAS\tAGENTES")
	for _, s := range list {
		taskCount := 0
		if s.Plan != nil {
			taskCount = len(s.Plan.Tasks)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", s.RunID, s.Phase, s.IntegrationBranch, taskCount, len(s.Agents))
	}
	return tw.Flush()
}
