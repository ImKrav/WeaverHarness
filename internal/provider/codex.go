package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

type codexProvider struct {
	bin string
}

func newCodex(bin string) Provider {
	if bin == "" {
		bin = "codex"
	}
	return &codexProvider{bin: bin}
}

func (c *codexProvider) Name() string {
	return "codex"
}

func (c *codexProvider) Check(ctx context.Context) Health {
	resolved, err := exec.LookPath(c.bin)
	if err != nil {
		return Health{Installed: false, Detail: err.Error()}
	}

	h := Health{Installed: true}
	if out, err := exec.CommandContext(ctx, resolved, "--version").CombinedOutput(); err == nil || len(out) > 0 {
		h.Version = strings.TrimSpace(string(out))
	}

	loginOut, loginErr := exec.CommandContext(ctx, resolved, "login", "status").CombinedOutput()
	h.Detail = strings.TrimSpace(string(loginOut))
	h.LoggedIn = loginErr == nil
	return h
}

func (c *codexProvider) Start(ctx context.Context, req RunRequest) (Run, error) {
	if req.Mode == Orchestrator {
		return nil, errors.New("v1: solo claude puede ser orquestador")
	}

	prompt, err := prependSystemPrompt(req.Prompt, req.SystemPromptFile)
	if err != nil {
		return nil, err
	}

	args := buildCodexArgs(req)
	return startJSONL(ctx, jsonlConfig{
		Bin:        c.bin,
		Args:       args,
		Dir:        req.Dir,
		StdinData:  []byte(prompt),
		InitialSID: req.ResumeID,
		ParseLine:  parseCodexLine,
	})
}

func buildCodexArgs(req RunRequest) []string {
	if req.ResumeID != "" {
		args := []string{"exec", "resume", "--json"}
		if req.Model != "" {
			args = append(args, "-m", req.Model)
		}
		args = append(args, "-c", `approval_policy="never"`, "-c", `sandbox_mode="danger-full-access"`, req.ResumeID, "-")
		return args
	}

	args := []string{"exec", "--json"}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	if req.Dir != "" {
		args = append(args, "-C", req.Dir)
	}
	args = append(args, "-s", "danger-full-access", "-c", `approval_policy="never"`, "-")
	return args
}

func parseCodexLine(line []byte, state *jsonlState) []Event {
	var raw struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Message  string `json:"message"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Message string `json:"message"`
			Command string `json:"command"`
			Tool    string `json:"tool"`
		} `json:"item"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	switch raw.Type {
	case "thread.started":
		if raw.ThreadID != "" {
			state.SessionID = raw.ThreadID
		}
		return nil
	case "item.completed":
		switch raw.Item.Type {
		case "agent_message":
			if raw.Item.Text != "" {
				state.FinalText = raw.Item.Text
				return []Event{{Kind: EventText, Text: raw.Item.Text}}
			}
		case "command_execution", "file_change", "mcp_tool_call", "web_search":
			name := raw.Item.Tool
			if name == "" {
				name = raw.Item.Type
			}
			return []Event{{Kind: EventTool, Tool: name}}
		case "error":
			if raw.Item.Message != "" {
				return []Event{{Kind: EventError, Text: raw.Item.Message}}
			}
		}
		return nil
	case "turn.completed":
		state.HasFinal = true
		return nil
	case "error":
		if raw.Message != "" {
			state.FinalErr = &ProviderError{
				Class: Classify(raw.Message),
				Msg:   raw.Message,
			}
			return []Event{{Kind: EventError, Text: raw.Message}}
		}
		return nil
	case "turn.failed":
		state.HasFinal = true
		msg := raw.Error.Message
		if msg == "" {
			msg = raw.Message
		}
		state.FinalErr = &ProviderError{
			Class: Classify(msg),
			Msg:   msg,
		}
		return []Event{{Kind: EventError, Text: msg}}
	default:
		return nil
	}
}
