package provider

// Tipos de evento observados en codex-cli 0.169.1 (`codex exec --json`):
// - thread.started: {"type":"thread.started","thread_id":"<uuid>"}
// - turn.started: {"type":"turn.started"}
// - item.completed: {"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"..."}}
//   (en error de modelo también aparece item.type=="error" con item.message)
// - turn.completed: {"type":"turn.completed","usage":{...}}
// - error: {"type":"error","message":"..."}
// - turn.failed: {"type":"turn.failed","error":{"message":"..."}}

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexOrchestratorRejected(t *testing.T) {
	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatalf("New(codex) error: %v", err)
	}
	_, err = p.Start(context.Background(), RunRequest{
		Mode:  Orchestrator,
		Dir:   t.TempDir(),
		Model: "gpt-5.6-sol",
	})
	if err == nil || !strings.Contains(err.Error(), "v1: solo claude puede ser orquestador") {
		t.Fatalf("Start(Orchestrator) error = %v, want v1: solo claude puede ser orquestador", err)
	}
}

func TestCodexArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "codex_ok.jsonl"))

	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "gpt-5.6-sol",
		Prompt: "Responde solo OK",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("Wait() error: %+v", final.Err)
	}

	got := readJSONArgs(t, argsFile)
	want := []string{
		"exec",
		"--json",
		"-m", "gpt-5.6-sol",
		"-C", dir,
		"-s", "danger-full-access",
		"-c", `approval_policy="never"`,
		"-",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestCodexResumeArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "codex_ok.jsonl"))

	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:     Worker,
		Dir:      dir,
		Model:    "gpt-5.6-sol",
		Prompt:   "Entrega el recibo",
		ResumeID: "01a21670-5dc0-7ff1-b6c8-970699aa9d17",
	})
	if err != nil {
		t.Fatalf("Start(resume) error: %v", err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("Wait() error: %+v", final.Err)
	}

	got := readJSONArgs(t, argsFile)
	want := []string{
		"exec", "resume",
		"--json",
		"-m", "gpt-5.6-sol",
		"-c", `approval_policy="never"`,
		"-c", `sandbox_mode="danger-full-access"`,
		"01a21670-5dc0-7ff1-b6c8-970699aa9d17",
		"-",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("resume args = %v, want %v", got, want)
	}
}

func TestCodexEvents(t *testing.T) {
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "codex_ok.jsonl"))

	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "gpt-5.6-sol",
		Prompt: "Responde solo OK",
	})
	if err != nil {
		t.Fatal(err)
	}

	var texts []string
	for ev := range run.Events() {
		if ev.Kind == EventText {
			texts = append(texts, ev.Text)
		}
	}
	final := run.Wait()
	if final.Err != nil {
		t.Fatalf("final.Err = %+v, want nil", final.Err)
	}
	if final.Text != "OK" {
		t.Fatalf("final.Text = %q, want %q", final.Text, "OK")
	}
	wantSID := "01a21670-5dc0-7ff1-b6c8-970699aa9d17"
	if final.SessionID != wantSID || run.SessionID() != wantSID {
		t.Fatalf("SessionID = (%q, %q), want %q", final.SessionID, run.SessionID(), wantSID)
	}
	if !slices.Equal(texts, []string{"OK"}) {
		t.Fatalf("texts = %v, want [OK]", texts)
	}
}

func TestCodexFail(t *testing.T) {
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "codex_fail.jsonl"))
	t.Setenv("FAKECLI_EXIT", "1")

	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "modelo-inexistente",
		Prompt: "Responde solo OK",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	final := run.Wait()
	if final.Err == nil || final.Err.Class != ModelUnknown {
		t.Fatalf("final.Err = %+v, want ModelUnknown", final.Err)
	}
}

func TestCodexSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "sys.md")
	if err := os.WriteFile(sysPath, []byte("INSTRUCCIONES DEL SISTEMA"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdinFile := filepath.Join(dir, "stdin.txt")
	t.Setenv("FAKECLI_STDIN", stdinFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "codex_ok.jsonl"))

	p, err := New("codex", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:             Worker,
		Dir:              dir,
		Model:            "gpt-5.6-sol",
		Prompt:           "TAREA REAL",
		SystemPromptFile: sysPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("final.Err = %+v", final.Err)
	}

	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	sysIdx := strings.Index(s, "INSTRUCCIONES DEL SISTEMA")
	promptIdx := strings.Index(s, "TAREA REAL")
	if sysIdx < 0 || promptIdx < 0 || sysIdx >= promptIdx {
		t.Fatalf("expected system prompt before prompt in stdin, got %q", s)
	}
}
