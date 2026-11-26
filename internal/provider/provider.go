package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ErrClass categorizes provider execution failures without inspecting vendor internals.
type ErrClass string

const (
	NotInstalled   ErrClass = "not_installed"
	NotLoggedIn    ErrClass = "not_logged_in"
	QuotaExhausted ErrClass = "quota_exhausted"
	ModelUnknown   ErrClass = "model_unknown"
	Timeout        ErrClass = "timeout"
	EmptyOutput    ErrClass = "empty_output"
	Crashed        ErrClass = "crashed"
)

// ProviderError pairs a normalized ErrClass with the underlying detail message.
type ProviderError struct {
	Class ErrClass
	Msg   string
}

// Error implements the error interface.
func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Msg == "" {
		return string(e.Class)
	}
	return fmt.Sprintf("%s: %s", e.Class, e.Msg)
}

// EventKind identifies normalized stream events emitted by provider runs.
type EventKind string

const (
	EventText    EventKind = "text"
	EventTool    EventKind = "tool"
	EventTurnEnd EventKind = "turn_end"
	EventError   EventKind = "error"
	EventRaw     EventKind = "raw"
)

// Event represents one normalized output event or raw JSONL line from a CLI run.
type Event struct {
	Kind EventKind
	Text string
	Tool string
	Raw  []byte
}

// Final summarizes the outcome of a completed CLI run.
type Final struct {
	Text      string
	SessionID string
	Err       *ProviderError
}

// Mode selects worker one-shot execution or long-lived orchestrator execution.
type Mode int

const (
	Worker Mode = iota
	Orchestrator
)

// RunRequest configures one provider CLI invocation.
type RunRequest struct {
	Mode             Mode
	Dir              string
	Model            string
	Prompt           string
	SystemPromptFile string
	SessionID        string
	ResumeID         string
	MCPConfigFile    string
	AllowedTools     []string
}

// Health describes CLI installation, authentication state, and known models.
type Health struct {
	Installed bool
	LoggedIn  bool
	Version   string
	Detail    string
	Models    []string
}

// Provider launches and health-checks one official vendor CLI binary.
type Provider interface {
	Name() string
	Check(ctx context.Context) Health
	Start(ctx context.Context, req RunRequest) (Run, error)
}

// Run represents a live or finished provider CLI process.
type Run interface {
	Events() <-chan Event
	Send(text string) error
	SessionID() string
	Wait() Final
	Kill() error
}

// New constructs a Provider adapter for name using bin (or the vendor default when empty).
func New(name, bin string) (Provider, error) {
	switch name {
	case "claude":
		return newClaude(bin), nil
	case "codex":
		return newCodex(bin), nil
	case "antigravity":
		return newAgy(bin), nil
	case "opencode":
		return newOpenCode(bin), nil
	default:
		return nil, fmt.Errorf("provider: proveedor desconocido %q", name)
	}
}

func prependSystemPrompt(prompt, systemPromptFile string) (string, error) {
	if systemPromptFile == "" {
		return prompt, nil
	}
	sysBytes, err := os.ReadFile(systemPromptFile)
	if err != nil {
		return "", &ProviderError{Class: Crashed, Msg: fmt.Sprintf("leyendo system prompt: %v", err)}
	}
	sysText := strings.TrimSpace(string(sysBytes))
	if sysText == "" {
		return prompt, nil
	}
	if prompt == "" {
		return sysText, nil
	}
	return sysText + "\n\n" + prompt, nil
}
