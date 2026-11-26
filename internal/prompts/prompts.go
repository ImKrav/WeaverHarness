package prompts

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
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
}

// Orchestrator returns the orchestrator system prompt, preferring a project override if present.
func Orchestrator(projectDir string) string {
	return loadPrompt(projectDir, "orchestrator.md", defaultOrchestrator)
}

// ReceiptRetry returns the retry prompt used when a worker omitted a valid receipt block.
func ReceiptRetry(projectDir string) string {
	return loadPrompt(projectDir, "receipt_retry.md", defaultReceiptRetry)
}

// Worker renders the worker prompt template with d, preferring a project override if present.
func Worker(projectDir string, d WorkerData) (string, error) {
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
	if projectDir != "" {
		override := filepath.Join(projectDir, ".weaver", "prompts", name)
		if data, err := os.ReadFile(override); err == nil {
			return string(data)
		}
	}
	return fallback
}
