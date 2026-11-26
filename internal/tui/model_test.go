package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/provider"
	tea "github.com/charmbracelet/bubbletea"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func TestEnterSends(t *testing.T) {
	var sent []string
	m := New("WeaverHarness", func(s string) error {
		sent = append(sent, s)
		return nil
	}, nil)

	m = typeText(m, "implementa login")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(sent) != 1 || sent[0] != "implementa login" {
		t.Fatalf("sent = %v, want [implementa login]", sent)
	}
	if !strings.Contains(m.View(), "tú › implementa login") {
		t.Fatalf("View() missing user message:\n%s", m.View())
	}
}

func TestOrchestratorTextRendered(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	next, _ := m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventText, Text: "Spec lista"}})
	m = next.(Model)
	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventTool, Tool: "Read"}})
	m = next.(Model)
	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventError, Text: "fallo menor"}})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "opus › Spec lista") {
		t.Fatalf("View() missing orchestrator text:\n%s", view)
	}
	if !strings.Contains(view, "· Read") {
		t.Fatalf("View() missing tool line:\n%s", view)
	}
	if !strings.Contains(view, "fallo menor") {
		t.Fatalf("View() missing error line:\n%s", view)
	}
}

func TestApprovalApprove(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	reply := make(chan ApprovalAnswer, 1)

	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "spec", Path: "docs/spec.md", Summary: "login"},
		Reply: reply,
	})
	m = next.(Model)

	if !strings.Contains(m.View(), "Aprobación: spec") {
		t.Fatalf("View() missing approval prompt:\n%s", m.View())
	}

	next, _ = m.Update(keyRunes("a"))
	_ = next.(Model)

	select {
	case ans := <-reply:
		if !ans.Approved || ans.Comment != "" {
			t.Fatalf("ApprovalAnswer = %+v, want {Approved:true Comment:\"\"}", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ApprovalAnswer")
	}
}

func TestApprovalRejectWithComment(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	reply := make(chan ApprovalAnswer, 1)

	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "plan"},
		Reply: reply,
	})
	m = next.(Model)

	next, _ = m.Update(keyRunes("r"))
	m = next.(Model)
	m = typeText(m, "falta T3")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = next.(Model)

	select {
	case ans := <-reply:
		if ans.Approved || ans.Comment != "falta T3" {
			t.Fatalf("ApprovalAnswer = %+v, want {Approved:false Comment:\"falta T3\"}", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rejection ApprovalAnswer")
	}
}

func TestAgentsPanel(t *testing.T) {
	okTrue := true
	okFalse := false
	var detailCalled string

	m := New("WeaverHarness", nil, func(id string) AgentDetail {
		detailCalled = id
		return AgentDetail{ID: id, Receipt: `{"status":"done"}`, Verification: `{"passed":true}`, LogTail: "log line"}
	})

	next, _ := m.Update(AgentsChanged{
		Agents: []engine.AgentInfo{
			{ID: "a1", TaskID: "T2", Provider: "antigravity", Model: "gemini-4-hydrogen", Status: "running", Started: time.Now().Add(-10 * time.Second)},
			{ID: "a2", TaskID: "T1", Provider: "codex", Model: "gpt-5.6-sol", Status: "finished", ReceiptOK: &okTrue, Verified: &okTrue},
			{ID: "a3", TaskID: "T3", Provider: "opencode", Model: "m", Status: "failed", Err: "no_receipt", ReceiptOK: &okFalse},
		},
	})
	m = next.(Model)

	view := m.View()
	for _, want := range []string{"●", "✓", "✗", "a1", "a2", "a3", "no_receipt"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}

	// Navigate down and open detail modal with Enter, close with Esc.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if detailCalled != "a2" {
		t.Fatalf("detail called with %q, want a2", detailCalled)
	}
	if !strings.Contains(m.View(), "log line") {
		t.Fatalf("modal View() missing LogTail:\n%s", m.View())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if strings.Contains(m.View(), "log line") {
		t.Fatalf("modal remained open after Esc:\n%s", m.View())
	}
}

func TestCtrlCConfirm(t *testing.T) {
	m := New("WeaverHarness", nil, nil)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("first Ctrl+C should ask confirmation, not quit immediately")
	}
	if !strings.Contains(m.View(), "¿Parar todo? [s/N]") {
		t.Fatalf("View() missing confirmation prompt:\n%s", m.View())
	}

	// Press 'n' cancels confirmation.
	next, cmd = m.Update(keyRunes("n"))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("pressing n should cancel quit")
	}
	if strings.Contains(m.View(), "¿Parar todo? [s/N]") {
		t.Fatalf("confirmation prompt remained after n:\n%s", m.View())
	}

	// Press Ctrl+C then 's' returns tea.Quit.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	_, cmd = m.Update(keyRunes("s"))
	if cmd == nil {
		t.Fatal("pressing s after Ctrl+C returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd() did not return tea.QuitMsg")
	}
}

func TestApproverCtxCancel(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	p := tea.NewProgram(
		m,
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)

	errCh := make(chan error, 1)
	go func() {
		_, runErr := p.Run()
		errCh <- runErr
	}()
	t.Cleanup(func() {
		p.Quit()
		<-errCh
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	app := Approver{P: p}
	approved, _, err := app.Ask(ctx, engine.ApprovalRequest{
		Kind:    "spec",
		Path:    "docs/spec.md",
		Summary: "s",
	})
	if approved {
		t.Fatalf("approved = true on cancelled ctx, want false")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ask() err = %v, want DeadlineExceeded", err)
	}
}

func TestRefViolationsAndOrchestratorExited(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	next, _ := m.Update(RefViolations{
		Items: nil,
	})
	m = next.(Model)
	_ = m

	next, _ = m.Update(OrchestratorExited{Err: errors.New("crashed")})
	m = next.(Model)
	if !strings.Contains(m.View(), "weaver resume") {
		t.Fatalf("View() missing weaver resume hint on exit:\n%s", m.View())
	}
}
