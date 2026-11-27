package prompts

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

//go:embed orchestrator.md
var defaultOrchestrator string

//go:embed worker.md
var defaultWorker string

//go:embed receipt_retry.md
var defaultReceiptRetry string

// WorkerData supplies task details rendered into the worker prompt template.
type WorkerData struct {
	TaskID       string
	Title        string
	SpecPath     string
	Branch       string
	Instructions string
	SpecRefs     []string
	Checks       []string
	TargetOS     string
	TargetShell  string
}

func platformContext(goos string) (osLabel, shellLabel string) {
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "windows":
		return "windows", "cmd /s /c"
	case "darwin":
		return "darwin (macOS)", "sh -c"
	case "linux":
		return "linux", "sh -c"
	default:
		return goos, "sh -c"
	}
}

// Orchestrator returns the orchestrator system prompt, preferring a project override if present.
func Orchestrator(projectDir string) string {
	if override, ok := loadPromptOverride(projectDir, "orchestrator.md"); ok {
		return override
	}
	osLabel, shellLabel := platformContext(runtime.GOOS)
	return fmt.Sprintf(
		"%s\n\n## Entorno de ejecución\n- **Sistema operativo:** %s (%s/%s)\n- **Shell de verificación:** `%s`\n",
		strings.TrimRight(defaultOrchestrator, "\r\n"),
		osLabel, runtime.GOOS, runtime.GOARCH, shellLabel,
	)
}

// ReceiptRetry returns the retry prompt used when a worker omitted a valid receipt block.
func ReceiptRetry(projectDir string) string {
	return loadPrompt(projectDir, "receipt_retry.md", defaultReceiptRetry)
}

// Worker renders the worker prompt template with d, preferring a project override if present.
func Worker(projectDir string, d WorkerData) (string, error) {
	defOS, defShell := platformContext(runtime.GOOS)
	if strings.TrimSpace(d.TargetOS) == "" {
		d.TargetOS = defOS
	}
	if strings.TrimSpace(d.TargetShell) == "" {
		d.TargetShell = defShell
	}

	raw := loadPrompt(projectDir, "worker.md", defaultWorker)
	tmpl, err := template.New("worker").Parse(raw)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func loadPrompt(projectDir, name, fallback string) string {
	if override, ok := loadPromptOverride(projectDir, name); ok {
		return override
	}
	return fallback
}

func loadPromptOverride(projectDir, name string) (string, bool) {
	if projectDir == "" {
		return "", false
	}
	override := filepath.Join(projectDir, ".weaver", "prompts", name)
	data, err := os.ReadFile(override)
	if err != nil {
		return "", false
	}
	return string(data), true
}
