package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNoReceipt is returned when no valid JSON weaver-receipt block is found.
var ErrNoReceipt = errors.New("no hay bloque weaver-receipt")

var receiptBlockRe = regexp.MustCompile("(?s)```weaver-receipt[ \\t]*\\r?\\n(.*?)\\r?\\n[ \\t]*```")

// Change describes a file modified by a worker task.
type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Check records a verification command executed by the worker.
type Check struct {
	Cmd        string `json:"cmd"`
	ExitCode   int    `json:"exit_code"`
	OutputTail string `json:"output_tail"`
}

// Receipt is the structured completion report returned by a subagent.
type Receipt struct {
	TaskID       string   `json:"task_id"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	SpecRefs     []string `json:"spec_refs"`
	Changes      []Change `json:"changes"`
	Commits      []string `json:"commits"`
	Verification []Check  `json:"verification"`
	OpenIssues   []string `json:"open_issues"`
}

// Extract returns the last ```weaver-receipt ... ``` fenced block that parses as JSON.
func Extract(text string) (Receipt, error) {
	matches := receiptBlockRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return Receipt{}, ErrNoReceipt
	}

	var lastErr error
	for i := len(matches) - 1; i >= 0; i-- {
		body := strings.TrimSpace(matches[i][1])
		var r Receipt
		if err := json.Unmarshal([]byte(body), &r); err == nil {
			return r, nil
		} else {
			lastErr = err
		}
	}
	return Receipt{}, fmt.Errorf("%w: %v", ErrNoReceipt, lastErr)
}

// Validate checks receipt schema constraints against taskID.
func (r Receipt) Validate(taskID string) error {
	if r.TaskID != taskID {
		return fmt.Errorf("receipt: task_id %q no coincide con %q", r.TaskID, taskID)
	}
	switch r.Status {
	case "done", "blocked", "failed":
	default:
		return fmt.Errorf("receipt: status inválido %q", r.Status)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return errors.New("receipt: summary vacío")
	}
	if words := len(strings.Fields(r.Summary)); words > 150 {
		return fmt.Errorf("receipt: summary tiene %d palabras (máximo 150)", words)
	}
	for i, c := range r.Verification {
		if strings.TrimSpace(c.Cmd) == "" {
			return fmt.Errorf("receipt: verification[%d].cmd vacío", i)
		}
	}
	for i, ch := range r.Changes {
		switch ch.Kind {
		case "added", "modified", "deleted":
		default:
			return fmt.Errorf("receipt: changes[%d].kind inválido %q", i, ch.Kind)
		}
	}
	return nil
}
