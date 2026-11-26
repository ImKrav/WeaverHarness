package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

type opencodeProvider struct {
	bin string
}

func newOpenCode(bin string) Provider {
	if bin == "" {
		bin = "opencode"
	}
	return &opencodeProvider{bin: bin}
}

func (o *opencodeProvider) Name() string {
	return "opencode"
}

func (o *opencodeProvider) Check(ctx context.Context) Health {
	resolved, err := exec.LookPath(o.bin)
	if err != nil {
		return Health{Installed: false, Detail: err.Error()}
	}

	h := Health{Installed: true}
	if out, err := exec.CommandContext(ctx, resolved, "--version").CombinedOutput(); err == nil || len(out) > 0 {
		h.Version = strings.TrimSpace(string(out))
	}

	authOut, authErr := exec.CommandContext(ctx, resolved, "auth", "list").CombinedOutput()
	h.Detail = strings.TrimSpace(string(authOut))
	if authErr == nil {
		for _, line := range strings.Split(h.Detail, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.Contains(strings.ToLower(trimmed), "no authenticated") {
				continue
			}
			h.LoggedIn = true
			break
		}
	}

	if modelsOut, err := exec.CommandContext(ctx, resolved, "models").CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(modelsOut), "\n") {
			m := strings.TrimSpace(line)
			if m != "" {
				h.Models = append(h.Models, m)
			}
		}
	}
	return h
}

func (o *opencodeProvider) Start(ctx context.Context, req RunRequest) (Run, error) {
	if req.Mode == Orchestrator {
		return nil, errors.New("v1: solo claude puede ser orquestador")
	}

	prompt, err := prependSystemPrompt(req.Prompt, req.SystemPromptFile)
	if err != nil {
		return nil, err
	}

	sid := req.ResumeID
	if sid == "" {
		sid = req.SessionID
	}
	if sid != "" && !strings.HasPrefix(sid, "ses") {
		sid = "ses_" + sid
	}

	args := buildOpenCodeArgs(req.Model, sid)
	var lastMsgID string
	return startJSONL(ctx, jsonlConfig{
		Bin:        o.bin,
		Args:       args,
		Dir:        req.Dir,
		StdinData:  []byte(prompt),
		InitialSID: sid,
		ParseLine: func(line []byte, state *jsonlState) []Event {
			return parseOpenCodeLine(line, state, &lastMsgID)
		},
	})
}

func buildOpenCodeArgs(model, sessionID string) []string {
	args := []string{"run", "--format", "json"}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, "--auto")
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	return args
}

func parseOpenCodeLine(line []byte, state *jsonlState, lastMsgID *string) []Event {
	var raw struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Type      string `json:"type"`
			MessageID string `json:"messageID"`
			Text      string `json:"text"`
			Reason    string `json:"reason"`
			Tool      string `json:"tool"`
			Name      string `json:"name"`
		} `json:"part"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Status  int    `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	if raw.SessionID != "" {
		state.SessionID = raw.SessionID
	}

	switch raw.Type {
	case "text":
		if raw.Part.Text == "" {
			return nil
		}
		if raw.Part.MessageID != "" && raw.Part.MessageID != *lastMsgID {
			*lastMsgID = raw.Part.MessageID
			state.FinalText = raw.Part.Text
		} else if raw.Part.MessageID != "" {
			state.FinalText += raw.Part.Text
		} else {
			state.FinalText = raw.Part.Text
		}
		return []Event{{Kind: EventText, Text: raw.Part.Text}}
	case "tool_use", "tool":
		toolName := raw.Part.Tool
		if toolName == "" {
			toolName = raw.Part.Name
		}
		if toolName != "" {
			return []Event{{Kind: EventTool, Tool: toolName}}
		}
		return nil
	case "step_finish":
		if raw.Part.Reason == "stop" {
			state.HasFinal = true
		}
		return nil
	case "error":
		msg := raw.Error.Message
		if msg == "" {
			msg = raw.Error.Type
		}
		state.HasFinal = true
		state.FinalErr = &ProviderError{
			Class: Classify(msg),
			Msg:   msg,
		}
		return []Event{{Kind: EventError, Text: msg}}
	default:
		return nil
	}
}
