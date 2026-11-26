package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAgyOrchestratorRejected(t *testing.T) {
	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatalf("New(antigravity) error: %v", err)
	}
	_, err = p.Start(context.Background(), RunRequest{
		Mode:  Orchestrator,
		Dir:   t.TempDir(),
		Model: "gemini-4-hydrogen",
	})
	if err == nil || !strings.Contains(err.Error(), "v1: solo claude puede ser orquestador") {
		t.Fatalf("Start(Orchestrator) error = %v, want v1: solo claude puede ser orquestador", err)
	}
}

func TestAgyArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	stdinFile := filepath.Join(dir, "stdin.jsonl")
	t.Setenv("FAKECLI_ACP", "1")
	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_STDIN", stdinFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "agy_ok.jsonl"))

	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "gemini-4-hydrogen",
		Prompt: "Crea hola.txt",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("Wait() error: %+v", final.Err)
	}

	gotArgs := readJSONArgs(t, argsFile)
	wantArgs := []string{"acp", "--dangerously-skip-permissions", "--sandbox"}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}

	rpcBytes, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	rpc := string(rpcBytes)
	for _, method := range []string{`"initialize"`, `"session/new"`, `"session/set_model"`, `"session/prompt"`} {
		if !strings.Contains(rpc, method) {
			t.Fatalf("expected RPC method %s in stdin:\n%s", method, rpc)
		}
	}
}

func TestAgyEvents(t *testing.T) {
	t.Setenv("FAKECLI_ACP", "1")
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "agy_ok.jsonl"))

	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "gemini-4-hydrogen",
		Prompt: "Crea hola.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	var texts, tools []string
	for ev := range run.Events() {
		switch ev.Kind {
		case EventText:
			texts = append(texts, ev.Text)
		case EventTool:
			tools = append(tools, ev.Tool)
		}
	}
	final := run.Wait()
	if final.Err != nil {
		t.Fatalf("final.Err = %+v, want nil", final.Err)
	}
	if final.Text != "Se ha creado hola.txt" {
		t.Fatalf("final.Text = %q, want %q", final.Text, "Se ha creado hola.txt")
	}
	wantSID := "d1de2822-a530-4715-b41b-4c94f809da2b"
	if final.SessionID != wantSID || run.SessionID() != wantSID {
		t.Fatalf("SessionID = (%q, %q), want %q", final.SessionID, run.SessionID(), wantSID)
	}
	if !slices.Equal(tools, []string{"write_file"}) {
		t.Fatalf("tools = %v, want [write_file]", tools)
	}
	if strings.Join(texts, "") != "Se ha creado hola.txt" {
		t.Fatalf("texts = %v", texts)
	}
}

func TestAgyEmptyIsFailure(t *testing.T) {
	t.Setenv("FAKECLI_EXIT", "0")

	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "gemini-4-hydrogen",
		Prompt: "Hola",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	final := run.Wait()
	if final.Err == nil || final.Err.Class != EmptyOutput {
		t.Fatalf("final.Err = %+v, want EmptyOutput", final.Err)
	}
}

func TestAgyResumeArgs(t *testing.T) {
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin.jsonl")
	t.Setenv("FAKECLI_ACP", "1")
	t.Setenv("FAKECLI_STDIN", stdinFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "agy_ok.jsonl"))

	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:     Worker,
		Dir:      dir,
		Model:    "gemini-4-hydrogen",
		Prompt:   "Entrega el recibo",
		ResumeID: "conv-resume-99",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	if final := run.Wait(); final.Err != nil {
		t.Fatalf("final.Err = %+v", final.Err)
	}

	data, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	var sawResume bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var msg struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal([]byte(line), &msg) == nil && msg.Method == "session/resume" {
			if msg.Params["sessionId"] == "conv-resume-99" {
				sawResume = true
			}
		}
	}
	if !sawResume {
		t.Fatalf("expected ACP session/resume for conv-resume-99 in:\n%s", string(data))
	}
}

func TestAgyInvalidModel(t *testing.T) {
	t.Setenv("FAKECLI_ACP", "1")
	t.Setenv("FAKECLI_ACP_BAD_MODEL", "1")

	p, err := New("antigravity", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "bad-model",
		Prompt: "Hola",
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
