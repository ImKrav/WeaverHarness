package provider

// Observaciones en opencode v2.4.1 (`opencode run --format json -m opencode/big-pickle --auto`):
// - Lee el prompt desde stdin correctamente (no hace falta pasarlo en argv).
// - `--session` exige que el ID comience con "ses" (si se omite, OpenCode genera `ses_...`).
// - Eventos JSONL observados:
//   - step_start: {"type":"step_start","sessionID":"ses_...","part":{"messageID":"msg_...","type":"step-start"}}
//   - text: {"type":"text","sessionID":"ses_...","part":{"messageID":"msg_...","type":"text","text":"..."}}
//   - step_finish: {"type":"step_finish","sessionID":"ses_...","part":{"reason":"stop",...}}
//   - error: {"type":"error","sessionID":"...","error":{"type":"...","message":"...","status":429}}

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOpenCodeOrchestratorRejected(t *testing.T) {
	p, err := New("opencode", fakeCLIBin)
	if err != nil {
		t.Fatalf("New(opencode) error: %v", err)
	}
	_, err = p.Start(context.Background(), RunRequest{
		Mode:  Orchestrator,
		Dir:   t.TempDir(),
		Model: "opencode/big-pickle",
	})
	if err == nil || !strings.Contains(err.Error(), "v1: solo claude puede ser orquestador") {
		t.Fatalf("Start(Orchestrator) error = %v, want v1: solo claude puede ser orquestador", err)
	}
}

func TestOpenCodeArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "opencode_ok.jsonl"))

	p, err := New("opencode", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:      Worker,
		Dir:       dir,
		Model:     "opencode/big-pickle",
		Prompt:    "Responde solo OK",
		SessionID: "ses_weavertest1",
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
		"run",
		"--format", "json",
		"-m", "opencode/big-pickle",
		"--auto",
		"--session", "ses_weavertest1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestOpenCodeEvents(t *testing.T) {
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "opencode_ok.jsonl"))

	p, err := New("opencode", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "opencode/big-pickle",
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
	wantSID := "ses_de98db558ffeAgDNiqWk1rKpUw"
	if final.SessionID != wantSID || run.SessionID() != wantSID {
		t.Fatalf("SessionID = (%q, %q), want %q", final.SessionID, run.SessionID(), wantSID)
	}
	if !slices.Equal(texts, []string{"OK"}) {
		t.Fatalf("texts = %v, want [OK]", texts)
	}
}

func TestOpenCodeLongPrompt(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	stdinFile := filepath.Join(dir, "stdin.txt")
	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_STDIN", stdinFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "opencode_ok.jsonl"))

	prompt := "Especiales: & | % \" ^\nSegunda línea\n" + strings.Repeat("Z", 40_000)

	p, err := New("opencode", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "opencode/big-pickle",
		Prompt: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("final.Err = %+v", final.Err)
	}

	gotStdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotStdin) != prompt {
		t.Fatalf("stdin len=%d != prompt len=%d", len(gotStdin), len(prompt))
	}

	args := readJSONArgs(t, argsFile)
	for _, a := range args {
		if strings.Contains(a, "Especiales:") || len(a) > 1000 {
			t.Fatalf("prompt leaked into argv: %q", a)
		}
	}
}
