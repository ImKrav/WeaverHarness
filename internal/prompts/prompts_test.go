package prompts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPromptsIncludeOSAndShellContext(t *testing.T) {
	cases := []struct {
		goos      string
		wantOS    string
		wantShell string
	}{
		{goos: "windows", wantOS: "windows", wantShell: "cmd /s /c"},
		{goos: "linux", wantOS: "linux", wantShell: "sh -c"},
		{goos: "darwin", wantOS: "darwin", wantShell: "sh -c"},
	}
	for _, tc := range cases {
		osLabel, shellLabel := platformContext(tc.goos)
		if !strings.Contains(strings.ToLower(osLabel), tc.wantOS) {
			t.Fatalf("platformContext(%q) osLabel = %q, want substring %q", tc.goos, osLabel, tc.wantOS)
		}
		if shellLabel != tc.wantShell {
			t.Fatalf("platformContext(%q) shellLabel = %q, want %q", tc.goos, shellLabel, tc.wantShell)
		}
	}

	wantOS, wantShell := platformContext(runtime.GOOS)

	orch := Orchestrator("")
	for _, want := range []string{wantOS, runtime.GOARCH, wantShell} {
		if !strings.Contains(orch, want) {
			t.Fatalf("Orchestrator(\"\") missing %q:\n%s", want, orch)
		}
	}

	worker, err := Worker("", WorkerData{
		TaskID: "T1",
		Title:  "Prueba",
		Branch: "weaver/r1/a1",
	})
	if err != nil {
		t.Fatalf("Worker() error: %v", err)
	}
	for _, want := range []string{wantOS, wantShell} {
		if !strings.Contains(worker, want) {
			t.Fatalf("Worker(\"\") missing %q:\n%s", want, worker)
		}
	}

	explicitWorker, err := Worker("", WorkerData{
		TaskID:      "T2",
		Title:       "Custom",
		Branch:      "weaver/r1/a2",
		TargetOS:    "darwin (macOS)",
		TargetShell: "sh -c",
	})
	if err != nil {
		t.Fatalf("Worker(explicit) error: %v", err)
	}
	if !strings.Contains(explicitWorker, "darwin (macOS)") || !strings.Contains(explicitWorker, "sh -c") {
		t.Fatalf("Worker(explicit) did not use provided TargetOS/TargetShell:\n%s", explicitWorker)
	}

	// Overrides under .weaver/prompts/ must remain intact.
	dir := t.TempDir()
	promptsDir := filepath.Join(dir, ".weaver", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orchOverride := "override orchestrator exacto"
	if err := os.WriteFile(filepath.Join(promptsDir, "orchestrator.md"), []byte(orchOverride), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Orchestrator(dir); got != orchOverride {
		t.Fatalf("Orchestrator(override) = %q, want %q", got, orchOverride)
	}

	workerOverride := "worker custom {{.TaskID}}"
	if err := os.WriteFile(filepath.Join(promptsDir, "worker.md"), []byte(workerOverride), 0o600); err != nil {
		t.Fatal(err)
	}
	gotWorker, err := Worker(dir, WorkerData{TaskID: "T9"})
	if err != nil {
		t.Fatalf("Worker(override) error: %v", err)
	}
	if gotWorker != "worker custom T9" {
		t.Fatalf("Worker(override) = %q, want %q", gotWorker, "worker custom T9")
	}
}
