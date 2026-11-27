package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/receipt"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

// InspectOptions configures app.Inspect.
type InspectOptions struct {
	Dir   string
	RunID string // vacío = última ejecución
	JSON  bool
	Out   io.Writer
}

// LogsOptions configures app.Logs.
type LogsOptions struct {
	Dir         string
	RunID       string // vacío = última ejecución
	AgentID     string // obligatorio
	Tail        int    // <= 0 usa 100
	OnlyReceipt bool   // omite transcript.jsonl
	Out         io.Writer
}

type inspectReport struct {
	State         *run.State                     `json:"state"`
	Receipts      map[string]receipt.Receipt     `json:"receipts,omitempty"`
	Verifications map[string]verify.Verification `json:"verifications,omitempty"`
}

func resolveRunState(dir, runID string) (run.Store, *run.State, error) {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return run.Store{}, nil, fmt.Errorf("app: leyendo directorio actual: %w", err)
		}
	}
	repo, err := gitx.Open(dir)
	if err != nil {
		return run.Store{}, nil, err
	}
	store := run.Store{Root: filepath.Join(repo.Dir, ".weaver", "runs")}
	var st *run.State
	if strings.TrimSpace(runID) == "" {
		st, err = store.Latest()
	} else {
		st, err = store.Load(runID)
	}
	if err != nil {
		return store, nil, err
	}
	return store, st, nil
}

// Inspect prints the full state, tasks, agents, receipts, and verifications of a run.
func Inspect(o InspectOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	store, st, err := resolveRunState(o.Dir, o.RunID)
	if err != nil {
		return err
	}

	receipts := make(map[string]receipt.Receipt)
	verifs := make(map[string]verify.Verification)
	for _, ag := range st.Agents {
		agDir := store.AgentDir(st.RunID, ag.ID)
		if data, err := os.ReadFile(filepath.Join(agDir, "receipt.json")); err == nil {
			var rc receipt.Receipt
			if json.Unmarshal(data, &rc) == nil {
				receipts[ag.ID] = rc
			}
		}
		if data, err := os.ReadFile(filepath.Join(agDir, "verification.json")); err == nil {
			var v verify.Verification
			if json.Unmarshal(data, &v) == nil {
				verifs[ag.ID] = v
			}
		}
	}

	if o.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(inspectReport{
			State:         st,
			Receipts:      receipts,
			Verifications: verifs,
		})
	}

	fmt.Fprintf(out, "Run ID:            %s\n", st.RunID)
	fmt.Fprintf(out, "Fase:              %s\n", st.Phase)
	fmt.Fprintf(out, "Rama base:         %s\n", st.BaseBranch)
	fmt.Fprintf(out, "Rama integración:  %s\n", st.IntegrationBranch)
	fmt.Fprintf(out, "Directorio sesión: %s\n", st.SessionDir)

	fmt.Fprintln(out, "\nAprobaciones:")
	if len(st.Approvals) == 0 {
		fmt.Fprintln(out, "  (ninguna)")
	} else {
		for _, a := range st.Approvals {
			estado := "rechazada"
			if a.Approved {
				estado = "aprobada"
			}
			line := fmt.Sprintf("  - [%s] %s (%s)", estado, a.Kind, a.Path)
			if a.Summary != "" {
				line += ": " + a.Summary
			}
			if a.Comment != "" {
				line += " [comentario: " + a.Comment + "]"
			}
			fmt.Fprintln(out, line)
		}
	}

	fmt.Fprintln(out, "\nTareas:")
	if st.Plan == nil || len(st.Plan.Tasks) == 0 {
		fmt.Fprintln(out, "  (ninguna)")
	} else {
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tROL\tESTADO\tRONDAS\tAGENTE")
		for _, t := range st.Plan.Tasks {
			agentCol := t.AcceptedAgent
			if agentCol == "" {
				agentCol = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", t.ID, t.Role, t.Status, t.Rounds, agentCol)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "\nAgentes:")
	if len(st.Agents) == 0 {
		fmt.Fprintln(out, "  (ninguno)")
	} else {
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTAREA\tPROVEEDOR/MODELO\tESTADO\tDURACIÓN\tRECIBO\tVERIF\tERROR")
		for _, ag := range st.Agents {
			rcCol := "-"
			if rc, ok := receipts[ag.ID]; ok {
				if rc.Status != "" {
					rcCol = rc.Status
				} else {
					rcCol = "ok"
				}
			}
			verCol := "-"
			if v, ok := verifs[ag.ID]; ok {
				if v.Passed {
					verCol = "ok"
				} else {
					verCol = "fail"
				}
			}
			errCol := ag.Err
			if errCol == "" {
				errCol = "-"
			}
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s/%s\t%s\t%s\t%s\t%s\t%s\n",
				ag.ID,
				ag.TaskID,
				ag.Provider,
				ag.Model,
				ag.Status,
				formatAgentDuration(ag),
				rcCol,
				verCol,
				errCol,
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	return nil
}

func formatAgentDuration(ag *run.Agent) string {
	if ag == nil || ag.StartedAt.IsZero() {
		return "-"
	}
	if !ag.EndedAt.IsZero() {
		return ag.EndedAt.Sub(ag.StartedAt).Truncate(time.Second).String()
	}
	if ag.Status == run.AgentQueued || ag.Status == run.AgentRunning {
		return time.Since(ag.StartedAt).Truncate(time.Second).String()
	}
	return "-"
}

// Logs prints an agent's receipt, verification, and optional transcript tail.
func Logs(o LogsOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if strings.TrimSpace(o.AgentID) == "" {
		return fmt.Errorf("logs: se requiere especificar <agent-id>")
	}
	store, st, err := resolveRunState(o.Dir, o.RunID)
	if err != nil {
		return err
	}
	ag := st.Agent(o.AgentID)
	if ag == nil {
		return fmt.Errorf("logs: agente %q no encontrado en la ejecución %s", o.AgentID, st.RunID)
	}

	agDir := store.AgentDir(st.RunID, ag.ID)
	fmt.Fprintf(out, "Run %s · Agente %s · tarea %s · %s/%s · estado %s\n",
		st.RunID, ag.ID, ag.TaskID, ag.Provider, ag.Model, ag.Status)

	fmt.Fprintln(out, "\n--- Recibo (receipt.json) ---")
	if rc := readTextFile(filepath.Join(agDir, "receipt.json")); rc != "" {
		fmt.Fprintln(out, rc)
	} else {
		fmt.Fprintln(out, "(no disponible)")
	}

	fmt.Fprintln(out, "\n--- Verificación (verification.json) ---")
	if vr := readTextFile(filepath.Join(agDir, "verification.json")); vr != "" {
		fmt.Fprintln(out, vr)
	} else {
		fmt.Fprintln(out, "(no disponible)")
	}

	if !o.OnlyReceipt {
		tail := o.Tail
		if tail <= 0 {
			tail = 100
		}
		fmt.Fprintf(out, "\n--- Transcript (últimas %d líneas de transcript.jsonl) ---\n", tail)
		if tr := readTailLines(filepath.Join(agDir, "transcript.jsonl"), tail); tr != "" {
			fmt.Fprintln(out, tr)
		} else {
			fmt.Fprintln(out, "(no disponible)")
		}
	}
	return nil
}
