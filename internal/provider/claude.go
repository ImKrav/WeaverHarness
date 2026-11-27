package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

type claudeProvider struct {
	bin string
}

func newClaude(bin string) Provider {
	if bin == "" {
		bin = "claude"
	}
	return &claudeProvider{bin: bin}
}

func (c *claudeProvider) Name() string {
	return "claude"
}

func (c *claudeProvider) Check(ctx context.Context) Health {
	resolved, err := resolveBinary(c.Name(), c.bin)
	if err != nil {
		return Health{Installed: false, Detail: err.Error()}
	}

	h := Health{Installed: true}
	if out, err := exec.CommandContext(ctx, resolved, "--version").CombinedOutput(); err == nil {
		h.Version = strings.TrimSpace(string(out))
	} else {
		h.Version = strings.TrimSpace(string(out))
	}

	authOut, authErr := exec.CommandContext(ctx, resolved, "auth", "status").CombinedOutput()
	h.Detail = strings.TrimSpace(string(authOut))
	h.LoggedIn = authErr == nil
	return h
}

func (c *claudeProvider) Start(ctx context.Context, req RunRequest) (Run, error) {
	args := buildClaudeArgs(req)
	initialSID := req.SessionID
	if initialSID == "" {
		initialSID = req.ResumeID
	}

	cfg := jsonlConfig{
		Provider:   c.Name(),
		Bin:        c.bin,
		Args:       args,
		Dir:        req.Dir,
		InitialSID: initialSID,
		ParseLine: func(line []byte, state *jsonlState) []Event {
			return parseClaudeLine(line, state, req.Mode == Orchestrator)
		},
	}

	if req.Mode == Orchestrator {
		cfg.KeepStdin = true
		cfg.FormatSend = formatClaudeUserMessage
	} else {
		cfg.StdinData = []byte(req.Prompt)
	}

	return startJSONL(ctx, cfg)
}

func buildClaudeArgs(req RunRequest) []string {
	args := []string{"-p"}
	if req.Mode == Orchestrator {
		args = append(args, "--input-format", "stream-json")
	}
	args = append(args, "--output-format", "stream-json", "--verbose")

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	if req.Mode == Orchestrator {
		if req.MCPConfigFile != "" {
			args = append(args, "--mcp-config", req.MCPConfigFile, "--strict-mcp-config")
		}
		if req.SystemPromptFile != "" {
			args = append(args, "--append-system-prompt-file", req.SystemPromptFile)
		}
		args = append(args, "--permission-mode", "dontAsk")
		if len(req.AllowedTools) > 0 {
			args = append(args, "--allowedTools")
			args = append(args, req.AllowedTools...)
		}
	} else {
		args = append(args, "--dangerously-skip-permissions")
		if req.SystemPromptFile != "" {
			args = append(args, "--append-system-prompt-file", req.SystemPromptFile)
		}
	}

	if req.ResumeID != "" {
		args = append(args, "--resume", req.ResumeID)
	} else if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}

	return args
}

func formatClaudeUserMessage(text string) ([]byte, error) {
	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{
		Type: "user",
	}
	msg.Message.Role = "user"
	msg.Message.Content = text

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func parseClaudeLine(line []byte, state *jsonlState, isOrchestrator bool) []Event {
	var raw struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
		Result    string `json:"result"`
		Message   struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	if raw.SessionID != "" {
		state.SessionID = raw.SessionID
	}

	switch raw.Type {
	case "system":
		return nil
	case "assistant":
		var out []Event
		for _, block := range raw.Message.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					out = append(out, Event{Kind: EventText, Text: block.Text})
				}
			case "tool_use":
				if block.Name != "" {
					out = append(out, Event{Kind: EventTool, Tool: block.Name})
				}
			}
		}
		return out
	case "result":
		state.HasFinal = true
		state.FinalText = raw.Result
		var out []Event
		if raw.IsError {
			state.FinalErr = &ProviderError{
				Class: Classify(raw.Result),
				Msg:   raw.Result,
			}
			out = append(out, Event{Kind: EventError, Text: raw.Result})
		}
		if isOrchestrator {
			out = append(out, Event{Kind: EventTurnEnd})
		}
		return out
	default:
		return nil
	}
}
