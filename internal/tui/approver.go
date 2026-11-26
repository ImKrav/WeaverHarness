package tui

import (
	"context"
	"errors"

	"github.com/ImKrav/weaverharness/internal/engine"
	tea "github.com/charmbracelet/bubbletea"
)

// Approver implements engine.Approver by dispatching approval prompts to a Bubble Tea program.
type Approver struct {
	P *tea.Program
}

type approvalCancelledMsg struct {
	reply chan<- ApprovalAnswer
}

// Ask requests human approval through the TUI and returns immediately if ctx cancels.
func (a Approver) Ask(ctx context.Context, req engine.ApprovalRequest) (bool, string, error) {
	if a.P == nil {
		return false, "", errors.New("tui: programa no inicializado")
	}
	reply := make(chan ApprovalAnswer, 1)
	a.P.Send(ApprovalAsked{Req: req, Reply: reply})

	select {
	case ans := <-reply:
		return ans.Approved, ans.Comment, nil
	case <-ctx.Done():
		a.P.Send(approvalCancelledMsg{reply: reply})
		return false, "", ctx.Err()
	}
}
