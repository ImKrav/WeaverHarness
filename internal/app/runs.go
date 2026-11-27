package app

import (
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"
	"time"

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
		_, _ = fmt.Fprintln(out, "No hay ejecuciones guardadas.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN ID\tFASE\tRAMA\tTAREAS\tAGENTES\tDURACIÓN")
	for _, s := range list {
		taskCount := 0
		if s.Plan != nil {
			taskCount = len(s.Plan.Tasks)
		}
		dur := "-"
		if len(s.Agents) > 0 {
			m := run.Summarize(s)
			if m.ActiveDuration > 0 {
				if m.ActiveDuration < time.Second {
					dur = m.ActiveDuration.Truncate(time.Millisecond).String()
				} else {
					dur = m.ActiveDuration.Truncate(time.Second).String()
				}
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", s.RunID, s.Phase, s.IntegrationBranch, taskCount, len(s.Agents), dur)
	}
	return tw.Flush()
}
