package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func absFixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readJSONArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	return args
}

func TestClaudeWorkerArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	fixFile := absFixture(t, "claude_ok.jsonl")

	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_FIXTURE", fixFile)

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if p.Name() != "claude" {
		t.Fatalf("Name() = %q, want claude", p.Name())
	}

	run, err := p.Start(context.Background(), RunRequest{
		Mode:             Worker,
		Dir:              dir,
		Model:            "opus",
		Prompt:           "hola",
		SystemPromptFile: "/tmp/sys.md",
		SessionID:        "sess-123",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	for range run.Events() {
	}
	final := run.Wait()
	if final.Err != nil {
		t.Fatalf("Wait() unexpected err: %+v", final.Err)
	}

	got := readJSONArgs(t, argsFile)
	want := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "opus",
		"--dangerously-skip-permissions",
		"--append-system-prompt-file", "/tmp/sys.md",
		"--session-id", "sess-123",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	if slices.Contains(got, "--bare") {
		t.Fatalf("args must never contain --bare: %v", got)
	}

	// Verify --resume replaces --session-id when ResumeID is set.
	argsFile2 := filepath.Join(dir, "args2.json")
	t.Setenv("FAKECLI_ARGS", argsFile2)
	run2, err := p.Start(context.Background(), RunRequest{
		Mode:     Worker,
		Dir:      dir,
		Model:    "sonnet",
		Prompt:   "sigue",
		ResumeID: "sess-123",
	})
	if err != nil {
		t.Fatalf("Start(resume) error: %v", err)
	}
	for range run2.Events() {
	}
	_ = run2.Wait()

	got2 := readJSONArgs(t, argsFile2)
	want2 := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "sonnet",
		"--dangerously-skip-permissions",
		"--resume", "sess-123",
	}
	if !slices.Equal(got2, want2) {
		t.Fatalf("resume args = %v, want %v", got2, want2)
	}
}

func TestClaudeWorkerEvents(t *testing.T) {
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "claude_ok.jsonl"))

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "opus",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}

	var texts, tools []string
	var rawCount int
	for ev := range run.Events() {
		switch ev.Kind {
		case EventRaw:
			rawCount++
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
	if final.Text != "Listo" || final.SessionID != "s-1" || run.SessionID() != "s-1" {
		t.Fatalf("final = %+v, run.SessionID() = %q", final, run.SessionID())
	}
	if !slices.Equal(texts, []string{"Hola"}) {
		t.Fatalf("texts = %v, want [Hola]", texts)
	}
	if !slices.Equal(tools, []string{"Bash"}) {
		t.Fatalf("tools = %v, want [Bash]", tools)
	}
	if rawCount != 4 {
		t.Fatalf("rawCount = %d, want 4", rawCount)
	}
}

func TestClaudeQuota(t *testing.T) {
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "claude_quota.jsonl"))
	t.Setenv("FAKECLI_EXIT", "1")

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "opus",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	final := run.Wait()
	if final.Err == nil || final.Err.Class != QuotaExhausted {
		t.Fatalf("final.Err = %+v, want QuotaExhausted", final.Err)
	}
}

func TestEmptyOutputExit0(t *testing.T) {
	t.Setenv("FAKECLI_EXIT", "0")

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "opus",
		Prompt: "hi",
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

func TestNotInstalled(t *testing.T) {
	p, err := New("claude", "weaver-definitely-not-installed-binary-xyz")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    t.TempDir(),
		Model:  "opus",
		Prompt: "hi",
	})
	if err == nil {
		t.Fatal("Start() succeeded, want NotInstalled")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Class != NotInstalled {
		t.Fatalf("err = %v, want *ProviderError{Class: NotInstalled}", err)
	}
}

func TestHugeLineAndCRLF(t *testing.T) {
	dir := t.TempDir()
	fixPath := filepath.Join(dir, "huge.jsonl")

	hugeText := strings.Repeat("x", 1_500_000)
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s-huge","model":"opus"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + hugeText + `"}]},"session_id":"s-huge"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"OK","session_id":"s-huge"}`,
	}
	content := strings.Join(lines, "\r\n") + "\r\n"
	if err := os.WriteFile(fixPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKECLI_FIXTURE", fixPath)

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "opus",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotHuge string
	for ev := range run.Events() {
		if ev.Kind == EventRaw && strings.ContainsRune(string(ev.Raw), '\r') {
			t.Fatalf("raw event contains trailing CR")
		}
		if ev.Kind == EventText {
			gotHuge = ev.Text
		}
	}
	final := run.Wait()
	if final.Err != nil {
		t.Fatalf("final.Err = %+v", final.Err)
	}
	if len(gotHuge) != 1_500_000 {
		t.Fatalf("len(gotHuge) = %d, want 1500000", len(gotHuge))
	}
}

func TestPromptViaStdin(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	stdinFile := filepath.Join(dir, "stdin.txt")

	t.Setenv("FAKECLI_ARGS", argsFile)
	t.Setenv("FAKECLI_STDIN", stdinFile)
	t.Setenv("FAKECLI_FIXTURE", absFixture(t, "claude_ok.jsonl"))

	prompt := "Especiales: & | % \" ^\nSegunda línea\n" + strings.Repeat("Z", 40_000)

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "opus",
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

func TestOrchestratorSend(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "orch-args.json")

	t.Setenv("FAKECLI_ECHO", "1")
	t.Setenv("FAKECLI_ARGS", argsFile)

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run, err := p.Start(ctx, RunRequest{
		Mode:             Orchestrator,
		Dir:              dir,
		Model:            "opus",
		SystemPromptFile: "/tmp/orch.md",
		MCPConfigFile:    "/tmp/mcp.json",
		SessionID:        "uuid-1",
		AllowedTools:     []string{"Read", "Bash(git *)", "mcp__weaver"},
	})
	if err != nil {
		t.Fatalf("Start(Orchestrator) error: %v", err)
	}

	waitTurn := func(wantSubstr string) {
		t.Helper()
		var gotText string
		for ev := range run.Events() {
			if ev.Kind == EventText {
				gotText = ev.Text
			}
			if ev.Kind == EventTurnEnd {
				if gotText != wantSubstr {
					t.Fatalf("turn text = %q, want %q", gotText, wantSubstr)
				}
				return
			}
		}
		t.Fatalf("events channel closed before turn_end for %q", wantSubstr)
	}

	if err := run.Send("hola uno"); err != nil {
		t.Fatalf("first Send error: %v", err)
	}
	waitTurn("eco: hola uno")

	args := readJSONArgs(t, argsFile)
	wantArgs := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "opus",
		"--mcp-config", "/tmp/mcp.json",
		"--strict-mcp-config",
		"--append-system-prompt-file", "/tmp/orch.md",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read", "Bash(git *)", "mcp__weaver",
		"--session-id", "uuid-1",
	}
	if !slices.Equal(args, wantArgs) {
		t.Fatalf("orchestrator args = %v, want %v", args, wantArgs)
	}

	if err := run.Send("hola dos"); err != nil {
		t.Fatalf("second Send error: %v", err)
	}
	waitTurn("eco: hola dos")

	_ = run.Kill()
	for range run.Events() {
	}
	_ = run.Wait()
}

func TestReadLineLimitedRejectsOversizedLine(t *testing.T) {
	// 2 MiB line with CRLF passes under 16 MiB default limit.
	twoMB := strings.Repeat("a", 2*1024*1024) + "\r\n"
	got, err := readLineLimited(bufio.NewReader(strings.NewReader(twoMB)), maxJSONLLineBytes)
	if err != nil {
		t.Fatalf("readLineLimited(2MB) error = %v, want nil", err)
	}
	if len(got) != len(twoMB) {
		t.Fatalf("readLineLimited(2MB) len = %d, want %d", len(got), len(twoMB))
	}

	// Oversized line exceeding configured limit returns errLineTooLong.
	over := strings.Repeat("b", 2048) + "\n"
	_, err = readLineLimited(bufio.NewReader(strings.NewReader(over)), 1024)
	if !errors.Is(err, errLineTooLong) {
		t.Fatalf("readLineLimited(over) err = %v, want errLineTooLong", err)
	}

	// End-to-end jsonlRun terminates child and returns Crashed without goroutine leak.
	origLimit := jsonLineLimit
	jsonLineLimit = 512
	t.Cleanup(func() { jsonLineLimit = origLimit })

	dir := t.TempDir()
	fixPath := filepath.Join(dir, "oversized.jsonl")
	hugeLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("z", 2048) + `"}]},"session_id":"s-over"}` + "\n"
	if err := os.WriteFile(fixPath, []byte(hugeLine), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLI_FIXTURE", fixPath)

	p, err := New("claude", fakeCLIBin)
	if err != nil {
		t.Fatal(err)
	}
	baseG := runtime.NumGoroutine()
	r, err := p.Start(context.Background(), RunRequest{
		Mode:   Worker,
		Dir:    dir,
		Model:  "opus",
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range r.Events() {
	}
	final := r.Wait()
	if final.Err == nil || final.Err.Class != Crashed {
		t.Fatalf("final.Err = %+v, want Crashed", final.Err)
	}

	for i := 0; i < 20 && runtime.NumGoroutine() > baseG; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if gotG := runtime.NumGoroutine(); gotG > baseG {
		t.Fatalf("goroutine leak after oversized JSONL line: before=%d after=%d", baseG, gotG)
	}
}
