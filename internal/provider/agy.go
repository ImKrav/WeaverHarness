package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ImKrav/weaverharness/internal/procx"
)

type agyProvider struct {
	bin string
}

func newAgy(bin string) Provider {
	if bin == "" {
		bin = "agy"
	}
	return &agyProvider{bin: bin}
}

func (a *agyProvider) Name() string {
	return "antigravity"
}

func (a *agyProvider) Check(ctx context.Context) Health {
	resolved, err := exec.LookPath(a.bin)
	if err != nil {
		return Health{Installed: false, Detail: err.Error()}
	}

	h := Health{Installed: true}
	if out, err := exec.CommandContext(ctx, resolved, "--version").CombinedOutput(); err == nil || len(out) > 0 {
		h.Version = strings.TrimSpace(string(out))
	}

	var stdout, stderr bytes.Buffer
	modelsCmd := exec.CommandContext(ctx, resolved, "models")
	modelsCmd.Stdout = &stdout
	modelsCmd.Stderr = &stderr
	modelsErr := modelsCmd.Run()

	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			h.Models = append(h.Models, fields[0])
		}
	}

	errText := strings.TrimSpace(stderr.String())
	if modelsErr == nil && len(h.Models) > 0 {
		h.LoggedIn = true
		h.Detail = fmt.Sprintf("%d modelos disponibles", len(h.Models))
		return h
	}

	if modelsErr != nil {
		h.LoggedIn = false
		if errText != "" {
			h.Detail = errText
		} else {
			h.Detail = modelsErr.Error()
		}
		return h
	}

	h.LoggedIn = false
	h.Detail = errText
	return h
}

func (a *agyProvider) Start(ctx context.Context, req RunRequest) (Run, error) {
	if req.Mode == Orchestrator {
		return nil, errors.New("v1: solo claude puede ser orquestador")
	}

	resolvedBin, err := exec.LookPath(a.bin)
	if err != nil {
		return nil, &ProviderError{Class: NotInstalled, Msg: err.Error()}
	}

	prompt, err := prependSystemPrompt(req.Prompt, req.SystemPromptFile)
	if err != nil {
		return nil, err
	}

	args := []string{"acp", "--dangerously-skip-permissions", "--sandbox"}
	cmd := exec.Command(resolvedBin, args...)
	cmd.Dir = req.Dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ProviderError{Class: Crashed, Msg: fmt.Sprintf("stdout pipe: %v", err)}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, &ProviderError{Class: Crashed, Msg: fmt.Sprintf("stdin pipe: %v", err)}
	}

	stderrBuf := &stderrTailBuffer{limit: maxStderrTailBytes}
	cmd.Stderr = stderrBuf

	proc, err := procx.Start(cmd)
	if err != nil {
		_ = stdin.Close()
		return nil, &ProviderError{Class: Crashed, Msg: err.Error()}
	}

	r := &agyRun{
		proc:      proc,
		events:    make(chan Event, eventBufferSize),
		done:      make(chan struct{}),
		stopEmit:  make(chan struct{}),
		stdin:     stdin,
		sessionID: req.ResumeID,
	}

	watchStop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Kill()
		case <-watchStop:
		}
	}()

	go r.drive(ctx, stdout, stderrBuf, req, prompt, watchStop)
	return r, nil
}

type agyRun struct {
	proc     *procx.Proc
	events   chan Event
	done     chan struct{}
	stopEmit chan struct{}
	stopOnce sync.Once

	stdinMu sync.Mutex
	stdin   io.WriteCloser

	mu        sync.Mutex
	sessionID string
	final     Final
}

func (r *agyRun) Events() <-chan Event {
	return r.events
}

func (r *agyRun) Send(string) error {
	return errors.New("provider: Send solo está permitido en modo Orchestrator")
}

func (r *agyRun) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

func (r *agyRun) Wait() Final {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.final
}

func (r *agyRun) Kill() error {
	r.stopOnce.Do(func() {
		close(r.stopEmit)
	})
	r.closeStdin()
	return r.proc.Kill()
}

func (r *agyRun) closeStdin() {
	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if r.stdin != nil {
		_ = r.stdin.Close()
		r.stdin = nil
	}
}

func (r *agyRun) emit(ev Event) {
	select {
	case r.events <- ev:
	case <-r.stopEmit:
	}
}

func (r *agyRun) sendRPC(id int, method string, params any) error {
	payload := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if r.stdin == nil {
		return errors.New("stdin cerrada")
	}
	_, err = r.stdin.Write(data)
	return err
}

type acpRPCMsg struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *agyRun) drive(
	ctx context.Context,
	stdout io.ReadCloser,
	stderrBuf *stderrTailBuffer,
	req RunRequest,
	prompt string,
	watchStop chan struct{},
) {
	defer close(r.done)
	defer close(r.events)
	defer close(watchStop)

	reader := bufio.NewReader(stdout)
	var sawLine bool
	var hasPromptResult bool
	var finalErr *ProviderError
	var accumText strings.Builder
	sid := req.ResumeID

	readMsg := func() (*acpRPCMsg, error) {
		for {
			line, err := readLineLimited(reader, jsonLineLimit)
			if len(line) > 0 {
				trimmed := bytes.TrimRight(line, "\r\n")
				sawLine = true
				rawCopy := make([]byte, len(trimmed))
				copy(rawCopy, trimmed)
				r.emit(Event{Kind: EventRaw, Raw: rawCopy})

				if len(trimmed) > 0 {
					var msg acpRPCMsg
					if json.Unmarshal(trimmed, &msg) == nil {
						return &msg, nil
					}
				}
			}
			if err != nil {
				if errors.Is(err, errLineTooLong) {
					finalErr = &ProviderError{Class: Crashed, Msg: err.Error()}
					_ = r.Kill()
				}
				return nil, err
			}
		}
	}

	waitResp := func(wantID int) (*acpRPCMsg, error) {
		for {
			msg, err := readMsg()
			if err != nil {
				return nil, err
			}
			if msg.Method == "session/update" {
				r.handleUpdate(msg.Params, &accumText)
				continue
			}
			if msg.ID != nil && *msg.ID == wantID {
				return msg, nil
			}
		}
	}

	runACP := func() {
		if err := r.sendRPC(1, "initialize", map[string]any{
			"protocolVersion":    1,
			"clientCapabilities": map[string]any{},
			"clientInfo":         map[string]any{"name": "weaver", "version": "1.0"},
		}); err != nil {
			return
		}
		initResp, err := waitResp(1)
		if err != nil {
			return
		}
		if initResp.Error != nil {
			finalErr = &ProviderError{Class: Classify(initResp.Error.Message), Msg: initResp.Error.Message}
			return
		}

		method := "session/new"
		params := map[string]any{
			"cwd":        req.Dir,
			"mcpServers": []any{},
		}
		if req.ResumeID != "" {
			method = "session/resume"
			params["sessionId"] = req.ResumeID
		}
		if err := r.sendRPC(2, method, params); err != nil {
			return
		}
		sessResp, err := waitResp(2)
		if err != nil {
			return
		}
		if sessResp.Error != nil {
			finalErr = &ProviderError{Class: Classify(sessResp.Error.Message), Msg: sessResp.Error.Message}
			return
		}
		var sessRes struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(sessResp.Result, &sessRes) == nil && sessRes.SessionID != "" {
			sid = sessRes.SessionID
			r.mu.Lock()
			r.sessionID = sid
			r.mu.Unlock()
		}

		if req.Model != "" {
			if err := r.sendRPC(3, "session/set_model", map[string]any{
				"sessionId": sid,
				"modelId":   req.Model,
			}); err != nil {
				return
			}
			modelResp, err := waitResp(3)
			if err != nil {
				return
			}
			if modelResp.Error != nil {
				finalErr = &ProviderError{Class: Classify(modelResp.Error.Message), Msg: modelResp.Error.Message}
				r.emit(Event{Kind: EventError, Text: modelResp.Error.Message})
				return
			}
		}

		if err := r.sendRPC(4, "session/prompt", map[string]any{
			"sessionId": sid,
			"prompt": []map[string]any{
				{"type": "text", "text": prompt},
			},
		}); err != nil {
			return
		}
		promptResp, err := waitResp(4)
		if err != nil {
			return
		}
		if promptResp.Error != nil {
			finalErr = &ProviderError{Class: Classify(promptResp.Error.Message), Msg: promptResp.Error.Message}
			r.emit(Event{Kind: EventError, Text: promptResp.Error.Message})
			return
		}
		hasPromptResult = true
	}

	runACP()
	r.closeStdin()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- r.proc.Wait()
	}()

	var waitErr error
	timer := time.NewTimer(250 * time.Millisecond)
	select {
	case waitErr = <-waitCh:
		timer.Stop()
	case <-timer.C:
		_ = r.proc.Kill()
		waitErr = <-waitCh
	}

	code := procExitCode(waitErr)
	stderrStr := strings.TrimSpace(stderrBuf.String())
	text := accumText.String()

	final := Final{
		Text:      text,
		SessionID: sid,
	}

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		final.Err = &ProviderError{Class: Timeout, Msg: "tiempo de espera agotado"}
	case finalErr != nil:
		final.Err = finalErr
	case !sawLine:
		if code != 0 && stderrStr != "" {
			final.Err = &ProviderError{Class: Classify(stderrStr), Msg: stderrStr}
		} else {
			final.Err = &ProviderError{Class: EmptyOutput, Msg: "el CLI terminó sin salida"}
		}
	case !hasPromptResult:
		if code != 0 && stderrStr != "" {
			final.Err = &ProviderError{Class: Classify(stderrStr), Msg: stderrStr}
		} else {
			final.Err = &ProviderError{Class: EmptyOutput, Msg: "agy acp terminó sin completar el turno"}
		}
	case strings.TrimSpace(text) == "":
		final.Err = &ProviderError{Class: EmptyOutput, Msg: "agy acp terminó con respuesta vacía"}
	}

	r.mu.Lock()
	r.final = final
	r.mu.Unlock()
}

func (r *agyRun) handleUpdate(rawParams json.RawMessage, accum *strings.Builder) {
	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Name          string `json:"name"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return
	}
	if params.SessionID != "" {
		r.mu.Lock()
		r.sessionID = params.SessionID
		r.mu.Unlock()
	}

	switch params.Update.SessionUpdate {
	case "agent_message_chunk":
		if params.Update.Content.Type == "text" && params.Update.Content.Text != "" {
			accum.WriteString(params.Update.Content.Text)
			r.emit(Event{Kind: EventText, Text: params.Update.Content.Text})
		}
	case "tool_call":
		if params.Update.Name != "" {
			r.emit(Event{Kind: EventTool, Tool: params.Update.Name})
		}
	}
}
