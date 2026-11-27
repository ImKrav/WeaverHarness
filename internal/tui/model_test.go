package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	if !strings.Contains(view, "orquestador › Spec lista") {
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
	var cmd tea.Cmd
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(Model)
	}

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

func TestDynamicOrchestratorPrefix(t *testing.T) {
	m := NewWithOptions(Options{
		Title:            "WeaverHarness",
		OrchestratorName: "sonnet",
	})
	next, _ := m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventText, Text: "hola"}})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "sonnet › hola") {
		t.Fatalf("View() missing dynamic prefix sonnet › hola:\n%s", view)
	}
	if strings.Contains(view, "opus › ") {
		t.Fatalf("View() contains hardcoded opus prefix:\n%s", view)
	}
}

func TestThinkingSpinnerAndStatusBar(t *testing.T) {
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		Send:  func(string) error { return nil },
		InitialProgress: engine.RunProgress{
			RunID:         "20261127-100000",
			Phase:         run.PhasePlanApproved,
			TasksTotal:    3,
			TasksAccepted: 1,
		},
	})

	m = typeText(m, "arranca")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.thinking {
		t.Fatal("m.thinking = false after Enter, want true")
	}
	if cmd == nil {
		t.Fatal("expected spinner tick cmd after Enter when thinking starts")
	}

	view := m.View()
	if !strings.Contains(view, "plan_approved · 1/3 tareas") {
		t.Fatalf("View() missing status bar progress:\n%s", view)
	}
	if sp := m.spinner.View(); sp == "" || !strings.Contains(view, sp) {
		t.Fatalf("View() missing spinner %q:\n%s", sp, view)
	}

	tickMsg := m.spinner.Tick()
	next, tickCmd := m.Update(tickMsg)
	m = next.(Model)
	if tickCmd == nil {
		t.Fatal("expected next tick cmd while m.thinking == true")
	}

	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventTurnEnd}})
	m = next.(Model)
	if m.thinking {
		t.Fatal("m.thinking remained true after EventTurnEnd")
	}
	next, stopCmd := m.Update(spinner.TickMsg{})
	m = next.(Model)
	if stopCmd != nil {
		t.Fatal("spinner tick should not reschedule after EventTurnEnd")
	}

	// Verify OrchestratorExited also stops thinking.
	m = typeText(m, "otra")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.thinking {
		t.Fatal("m.thinking = false after second Enter")
	}
	next, _ = m.Update(OrchestratorExited{})
	m = next.(Model)
	if m.thinking {
		t.Fatal("m.thinking remained true after OrchestratorExited")
	}
	_, exitStopCmd := m.Update(spinner.TickMsg{})
	if exitStopCmd != nil {
		t.Fatal("spinner tick should not reschedule after OrchestratorExited")
	}
}

func TestSidebarShowsPlanTasks(t *testing.T) {
	var detailCalled string
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		Detail: func(agentID string) AgentDetail {
			detailCalled = agentID
			return AgentDetail{ID: agentID, LogTail: "detalle"}
		},
	})

	next, _ := m.Update(ProgressChanged{
		Progress: engine.RunProgress{
			RunID:         "20261127-100000",
			Phase:         run.PhasePlanApproved,
			TasksTotal:    3,
			TasksAccepted: 1,
			TasksRunning:  1,
			Tasks: []engine.TaskProgress{
				{ID: "T1", Title: "Primera", Role: "implementer", Status: run.TaskAccepted},
				{ID: "T2", Title: "Segunda", Role: "implementer", Status: run.TaskRunning},
				{ID: "T3", Title: "Tercera", Role: "implementer", Status: run.TaskPending},
			},
		},
	})
	m = next.(Model)

	next, _ = m.Update(AgentsChanged{
		Agents: []engine.AgentInfo{
			{ID: "a1", TaskID: "T1", Provider: "codex", Model: "m", Status: "finished"},
			{ID: "a2", TaskID: "T2", Provider: "codex", Model: "m", Status: "running"},
		},
	})
	m = next.(Model)

	view := m.View()
	idxTareas := strings.Index(view, "Tareas")
	idxAgentes := strings.Index(view, "Agentes")
	if idxTareas < 0 || idxAgentes < 0 || idxTareas >= idxAgentes {
		t.Fatalf("expected Tareas section above Agentes (tareas=%d agentes=%d):\n%s", idxTareas, idxAgentes, view)
	}
	for _, want := range []string{"✓ T1", "● T2", "○ T3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing task status %q:\n%s", want, view)
		}
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil {
		next, _ = m.Update(cmd())
		_ = next.(Model)
	}
	if detailCalled != "a2" {
		t.Fatalf("detailCalled = %q, want a2", detailCalled)
	}
}

func TestHeaderUpdatesOnNewRunID(t *testing.T) {
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		InitialProgress: engine.RunProgress{
			RunID: "run-1",
			Phase: run.PhaseOpen,
		},
	})
	if v := m.View(); !strings.Contains(v, "run-1") {
		t.Fatalf("initial View() missing run-1:\n%s", v)
	}

	next, _ := m.Update(ProgressChanged{
		Progress: engine.RunProgress{
			RunID: "run-2",
			Phase: run.PhaseSpecApproved,
		},
	})
	m = next.(Model)
	v := m.View()
	if !strings.Contains(v, "run-2") || strings.Contains(v, "run-1") {
		t.Fatalf("updated View() did not replace run-1 with run-2:\n%s", v)
	}
}

func TestChatWordWrapPreservesANSIAndWidth(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	chatW := m.viewport.Width
	longWords := strings.Repeat("palabra ", 25) // ~200 chars > chatW
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render(longWords)
	wrapped := wrapChatLines([]string{styled}, chatW)

	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected styled 200-char line to wrap at width %d, got %d lines", chatW, len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > chatW {
			t.Fatalf("line %d width = %d > chatW %d (%q)", i, w, chatW, l)
		}
	}

	// Verify viewport also receives wrapped output without exceeding chatW.
	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventError, Text: longWords}})
	m = next.(Model)
	for i, l := range strings.Split(m.viewport.View(), "\n") {
		if w := ansi.StringWidth(l); w > chatW {
			t.Fatalf("viewport line %d width = %d > chatW %d", i, w, chatW)
		}
	}
}

func TestSmartAutoScrollAndUnreadBadge(t *testing.T) {
	m := New("WeaverHarness", func(string) error { return nil }, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	for i := 1; i <= 40; i++ {
		next, _ = m.Update(OrchestratorEvent{Event: provider.Event{
			Kind: provider.EventText,
			Text: fmt.Sprintf("mensaje-%02d", i),
		}})
		m = next.(Model)
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("expected viewport at bottom initially")
	}

	// Scroll up; auto-scroll must stop and unreadBelow must increment on new events.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	if m.viewport.AtBottom() {
		t.Fatalf("expected viewport not at bottom after PgUp")
	}
	savedOffset := m.viewport.YOffset

	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventText, Text: "nuevo-1"}})
	m = next.(Model)
	if m.viewport.YOffset != savedOffset {
		t.Fatalf("YOffset moved from %d to %d while scrolled up", savedOffset, m.viewport.YOffset)
	}
	if m.unreadBelow != 1 {
		t.Fatalf("unreadBelow = %d, want 1", m.unreadBelow)
	}
	if !strings.Contains(m.View(), "↓ 1 nuevos") {
		t.Fatalf("View() missing unread badge ↓ 1 nuevos:\n%s", m.View())
	}

	// Pressing End jumps to bottom and clears unreadBelow.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(Model)
	if !m.viewport.AtBottom() || m.unreadBelow != 0 {
		t.Fatalf("after End: atBottom=%v unreadBelow=%d", m.viewport.AtBottom(), m.unreadBelow)
	}
	if strings.Contains(m.View(), "nuevos") {
		t.Fatalf("unread badge remained after End:\n%s", m.View())
	}

	// Scroll up again, add message, then sending a message resets unreadBelow.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	next, _ = m.Update(OrchestratorEvent{Event: provider.Event{Kind: provider.EventText, Text: "nuevo-2"}})
	m = next.(Model)
	if m.unreadBelow != 1 {
		t.Fatalf("unreadBelow = %d, want 1", m.unreadBelow)
	}
	m = typeText(m, "hola")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.unreadBelow != 0 || !m.viewport.AtBottom() {
		t.Fatalf("after Enter: atBottom=%v unreadBelow=%d", m.viewport.AtBottom(), m.unreadBelow)
	}
}

func TestApprovalKeysDoNotFireWhileTyping(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	reply := make(chan ApprovalAnswer, 1)

	m = typeText(m, "antes de aprobar")
	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "spec", Path: "docs/spec.md", Summary: "login"},
		Reply: reply,
	})
	m = next.(Model)

	for _, k := range []string{"a", "r", "v"} {
		next, _ = m.Update(keyRunes(k))
		m = next.(Model)
	}

	if m.pendingApproval == nil {
		t.Fatal("pendingApproval was unexpectedly resolved while typing in input")
	}
	if m.rejecting {
		t.Fatal("entering 'r' while typing triggered reject mode")
	}
	if got := m.input.Value(); got != "antes de aprobararv" {
		t.Fatalf("input.Value() = %q, want %q", got, "antes de aprobararv")
	}
	select {
	case ans := <-reply:
		t.Fatalf("unexpected ApprovalAnswer sent while typing: %+v", ans)
	default:
	}

	// With empty input, 'a' does approve.
	m.input.Reset()
	next, _ = m.Update(keyRunes("a"))
	m = next.(Model)
	select {
	case ans := <-reply:
		if !ans.Approved {
			t.Fatalf("expected Approved=true, got %+v", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval when input was empty")
	}
	if m.pendingApproval != nil {
		t.Fatal("pendingApproval remained non-nil after approval")
	}
}

func TestHelpFooterContextual(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	baseView := m.View()
	wantGeneral := "Tab panel · Ctrl+J nueva línea · Shift+arrastrar copiar · Ctrl+C salir"
	if !strings.Contains(baseView, wantGeneral) {
		t.Fatalf("View() missing general help footer %q:\n%s", wantGeneral, baseView)
	}

	reply := make(chan ApprovalAnswer, 1)
	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "p"},
		Reply: reply,
	})
	m = next.(Model)
	approvalView := m.View()
	wantApproval := "[a] aprobar  [r] rechazar  [v] ver detalle"
	if !strings.Contains(approvalView, wantApproval) {
		t.Fatalf("View() missing approval help footer %q:\n%s", wantApproval, approvalView)
	}
}

func TestModalScrollAndClose(t *testing.T) {
	var lines []string
	for i := 1; i <= 150; i++ {
		lines = append(lines, fmt.Sprintf("linea-%03d", i))
	}
	content := strings.Join(lines, "\n")

	m := New("WeaverHarness", nil, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m = next.(Model)

	next, _ = m.Update(modalLoadedMsg{title: "Vista previa", content: content})
	m = next.(Model)
	if m.modal == nil {
		t.Fatal("expected modal open after modalLoadedMsg")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.modal.vp.YOffset == 0 {
		t.Fatalf("expected YOffset > 0 after KeyDown")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)
	afterPgDown := m.modal.vp.YOffset
	if afterPgDown <= 1 {
		t.Fatalf("expected YOffset > 1 after KeyPgDown, got %d", afterPgDown)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.modal.vp.YOffset >= afterPgDown {
		t.Fatalf("expected YOffset to decrease after KeyUp (before=%d after=%d)", afterPgDown, m.modal.vp.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)

	// WindowSizeMsg resizes modal viewport.
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)
	if m.modal.vp.Width < 100 || m.modal.vp.Height < 18 {
		t.Fatalf("modal viewport did not resize: %dx%d", m.modal.vp.Width, m.modal.vp.Height)
	}

	// 'q' closes modal.
	next, _ = m.Update(keyRunes("q"))
	m = next.(Model)
	if m.modal != nil {
		t.Fatal("modal remained open after pressing q")
	}
}

func TestApprovalPlanSummaryRendered(t *testing.T) {
	m := New("WeaverHarness", nil, nil)
	reply := make(chan ApprovalAnswer, 1)

	plan := &run.Plan{
		Tasks: []*run.Task{
			{ID: "T1", Title: "Base", Role: "implementer", Checks: []string{"go test ./..."}},
			{ID: "T2", Title: "UI", Role: "reviewer", DependsOn: []string{"T1"}, Checks: []string{"go vet ./..."}},
		},
		GlobalChecks: []string{"go test ./...", "go vet ./..."},
	}
	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "plan", Plan: plan},
		Reply: reply,
	})
	m = next.(Model)

	view := m.View()
	for _, want := range []string{"T1", "T2", "global_checks"} {
		if !strings.Contains(view, want) {
			t.Fatalf("plan approval chat missing %q:\n%s", want, view)
		}
	}
}

func TestApprovalPreviewKeyVAsync(t *testing.T) {
	var previewReq *engine.ApprovalRequest
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		Preview: func(req engine.ApprovalRequest) (string, error) {
			cp := req
			previewReq = &cp
			return "# Spec preview\ncontenido largo", nil
		},
	})
	reply := make(chan ApprovalAnswer, 1)

	next, _ := m.Update(ApprovalAsked{
		Req:   engine.ApprovalRequest{Kind: "spec", Path: "docs/spec.md", Summary: "spec"},
		Reply: reply,
	})
	m = next.(Model)

	next, cmd := m.Update(keyRunes("v"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("pressing v with empty input returned nil cmd, want async preview cmd")
	}
	if m.modal != nil {
		t.Fatal("modal should not open synchronously before cmd executes")
	}

	loaded := cmd()
	if previewReq == nil || previewReq.Path != "docs/spec.md" {
		t.Fatalf("Preview callback got %+v", previewReq)
	}
	next, _ = m.Update(loaded)
	m = next.(Model)
	if m.modal == nil || !strings.Contains(m.View(), "contenido largo") {
		t.Fatalf("modal did not open with preview content:\n%s", m.View())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.modal != nil {
		t.Fatal("modal remained open after Esc")
	}
	if m.pendingApproval == nil {
		t.Fatal("pendingApproval was cleared after closing preview modal")
	}
}

func TestModalLiveRefreshWhileRunning(t *testing.T) {
	logText := "primera linea\nsegunda linea\ntercera linea"
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		Detail: func(agentID string) AgentDetail {
			return AgentDetail{ID: agentID, LogTail: logText}
		},
		InitialAgents: []engine.AgentInfo{
			{ID: "a1", TaskID: "T1", Provider: "codex", Model: "m", Status: "running"},
		},
	})

	// Resize small so log wraps/scrolls cleanly, open modal for a1.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 14})
	m = next.(Model)

	// Open modal with 30 lines and scroll to offset 2 (not at bottom).
	var initLines []string
	for i := 1; i <= 30; i++ {
		initLines = append(initLines, fmt.Sprintf("init-%02d", i))
	}
	logText = strings.Join(initLines, "\n")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected async detail cmd on Enter")
	}
	next, tickScheduleCmd := m.Update(cmd())
	m = next.(Model)
	if m.modal == nil || m.modal.agentID != "a1" {
		t.Fatalf("expected agent modal for a1, got %+v", m.modal)
	}
	if tickScheduleCmd == nil {
		t.Fatal("expected modal tick schedule while a1 is running")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	savedOffset := m.modal.vp.YOffset
	if savedOffset == 0 {
		t.Fatal("expected YOffset > 0 after scrolling down")
	}

	// Simulate new log appended while running; modalTickMsg reloads and preserves YOffset.
	logText += "\ninit-31\ninit-32"
	next, reloadCmd := m.Update(modalTickMsg{agentID: "a1"})
	m = next.(Model)
	if reloadCmd == nil {
		t.Fatal("modalTickMsg while running returned nil cmd")
	}
	next, nextTickCmd := m.Update(reloadCmd())
	m = next.(Model)
	if nextTickCmd == nil {
		t.Fatal("expected continued tick scheduling while running")
	}
	if m.modal.vp.YOffset != savedOffset {
		t.Fatalf("YOffset changed from %d to %d after live refresh", savedOffset, m.modal.vp.YOffset)
	}
	if !strings.Contains(m.modal.raw, "init-32") {
		t.Fatalf("modal content did not refresh with init-32")
	}

	// Now agent finishes -> modalTickMsg must stop scheduling ticks.
	next, _ = m.Update(AgentsChanged{
		Agents: []engine.AgentInfo{
			{ID: "a1", TaskID: "T1", Provider: "codex", Model: "m", Status: "finished"},
		},
	})
	m = next.(Model)
	next, stopCmd := m.Update(modalTickMsg{agentID: "a1"})
	m = next.(Model)
	if stopCmd != nil {
		t.Fatal("modalTickMsg continued scheduling after agent finished")
	}
}

func TestAgentsPanelScrollKeepsSelectionVisible(t *testing.T) {
	agents := make([]engine.AgentInfo, 20)
	for i := range agents {
		agents[i] = engine.AgentInfo{
			ID:       fmt.Sprintf("ag-%02d", i+1),
			TaskID:   fmt.Sprintf("T%02d", i+1),
			Provider: "codex",
			Model:    "m",
			Status:   "running",
		}
	}

	m := NewWithOptions(Options{
		Title:         "WeaverHarness",
		InitialAgents: agents,
	})
	// Height 20 => bodyH = max(6, 20-8) = 12.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	if _, _, _, bodyH := m.layout(); bodyH != 12 {
		t.Fatalf("bodyH = %d, want 12", bodyH)
	}

	// Move down 14 times -> selected index 14 (ag-15).
	for i := 0; i < 14; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
	}
	if m.selected != 14 {
		t.Fatalf("selected = %d, want 14", m.selected)
	}
	if m.panelOffset == 0 {
		t.Fatalf("panelOffset = 0, want > 0 after scrolling to agent 15")
	}

	view := m.View()
	if !strings.Contains(view, "ag-15") {
		t.Fatalf("View() missing selected scrolled agent ag-15:\n%s", view)
	}
	if !strings.Contains(view, "▲") || !strings.Contains(view, "▼") {
		t.Fatalf("View() missing scroll indicators ▲/▼:\n%s", view)
	}
}

func TestCancelAgentFromPanelAsync(t *testing.T) {
	var cancelledID string
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
		CancelAgent: func(agentID string) error {
			cancelledID = agentID
			return nil
		},
		InitialAgents: []engine.AgentInfo{
			{ID: "a1", TaskID: "T1", Provider: "codex", Model: "m", Status: "running"},
		},
	})

	// Focus panel on a1.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)

	// Press x -> prompts confirmation.
	next, cmd := m.Update(keyRunes("x"))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("x should prompt confirmation before calling CancelAgent")
	}
	if !strings.Contains(m.View(), "¿Cancelar a1? [s/N]") {
		t.Fatalf("View() missing cancel confirmation prompt:\n%s", m.View())
	}

	// Press n -> cancels confirmation.
	next, cmd = m.Update(keyRunes("n"))
	m = next.(Model)
	if cmd != nil || cancelledID != "" {
		t.Fatal("n should cancel confirmation without calling CancelAgent")
	}
	if strings.Contains(m.View(), "¿Cancelar a1? [s/N]") {
		t.Fatalf("confirmation prompt remained after n:\n%s", m.View())
	}

	// Press x then s -> returns async cmd calling Options.CancelAgent("a1").
	next, _ = m.Update(keyRunes("x"))
	m = next.(Model)
	next, cmd = m.Update(keyRunes("s"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("pressing s after cancel prompt returned nil cmd, want async CancelAgent cmd")
	}
	if cancelledID != "" {
		t.Fatal("CancelAgent called synchronously before cmd() ran")
	}

	msg := cmd()
	if cancelledID != "a1" {
		t.Fatalf("CancelAgent called with %q, want a1", cancelledID)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if !strings.Contains(m.View(), "cancelado") {
		t.Fatalf("View() missing cancellation confirmation in chat:\n%s", m.View())
	}

	// Error case from agentCancelledMsg also reflects in chat.
	next, _ = m.Update(agentCancelledMsg{agentID: "a1", err: errors.New("boom")})
	m = next.(Model)
	if !strings.Contains(m.View(), "error al cancelar a1: boom") {
		t.Fatalf("View() missing cancellation error in chat:\n%s", m.View())
	}
}

func TestMultilineInputCtrlJAndAltEnter(t *testing.T) {
	var sent []string
	m := New("WeaverHarness", func(s string) error {
		sent = append(sent, s)
		return nil
	}, nil)

	m = typeText(m, "linea uno")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = next.(Model)
	if cmd != nil || len(sent) != 0 {
		t.Fatalf("Ctrl+J sent message prematurely: sent=%v", sent)
	}

	m = typeText(m, "linea dos")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = next.(Model)
	if cmd != nil || len(sent) != 0 {
		t.Fatalf("Alt+Enter sent message prematurely: sent=%v", sent)
	}

	m = typeText(m, "linea tres")
	wantVal := "linea uno\nlinea dos\nlinea tres"
	if got := m.input.Value(); got != wantVal {
		t.Fatalf("input.Value() = %q, want %q", got, wantVal)
	}

	// Plain Enter sends full multiline text.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = next.(Model)
	if len(sent) != 1 || sent[0] != wantVal {
		t.Fatalf("sent = %q, want [%q]", sent, wantVal)
	}
}

func TestToggleMouseWithF2(t *testing.T) {
	m := NewWithOptions(Options{
		Title: "WeaverHarness",
	})
	if !m.mouseEnabled {
		t.Fatal("mouseEnabled = false by default, want true")
	}
	if !strings.Contains(m.View(), "F2 ratón: on") {
		t.Fatalf("View() missing 'F2 ratón: on':\n%s", m.View())
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = next.(Model)
	if m.mouseEnabled {
		t.Fatal("mouseEnabled remained true after first F2")
	}
	if cmd == nil {
		t.Fatal("first F2 returned nil cmd, want tea.DisableMouse")
	}
	vOff := m.View()
	if !strings.Contains(vOff, "F2 ratón: off") || !strings.Contains(vOff, "ratón desactivada") {
		t.Fatalf("View() missing off state / system chat note after first F2:\n%s", vOff)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = next.(Model)
	if !m.mouseEnabled {
		t.Fatal("mouseEnabled remained false after second F2")
	}
	if cmd == nil {
		t.Fatal("second F2 returned nil cmd, want tea.EnableMouseCellMotion")
	}
	vOn := m.View()
	if !strings.Contains(vOn, "F2 ratón: on") || !strings.Contains(vOn, "ratón activada") {
		t.Fatalf("View() missing on state / system chat note after second F2:\n%s", vOn)
	}

	mDisabled := NewWithOptions(Options{
		Title:         "WeaverHarness",
		MouseDisabled: true,
	})
	if mDisabled.mouseEnabled {
		t.Fatal("MouseDisabled: true should initialize mouseEnabled = false")
	}
}
