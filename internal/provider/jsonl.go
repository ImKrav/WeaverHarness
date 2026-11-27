package provider

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/ImKrav/weaverharness/internal/procx"
)

const (
	maxStderrTailBytes = 4096
	eventBufferSize    = 64
	maxJSONLLineBytes  = 16 * 1024 * 1024 // 16 MiB
)

var (
	errLineTooLong = errors.New("línea JSONL excede el tamaño máximo permitido")
	jsonLineLimit  = maxJSONLLineBytes
)

func readLineLimited(r *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(frag) > 0 {
			if limit > 0 && len(out)+len(frag) > limit {
				return nil, errLineTooLong
			}
			out = append(out, frag...)
		}
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return out, err
	}
}

type jsonlState struct {
	SessionID string
	FinalText string
	FinalErr  *ProviderError
	HasFinal  bool
	SawLine   bool
}

type jsonlConfig struct {
	Bin        string
	Args       []string
	Dir        string
	Env        []string
	StdinData  []byte
	KeepStdin  bool
	InitialSID string
	ParseLine  func(line []byte, state *jsonlState) []Event
	FormatSend func(text string) ([]byte, error)
}

type jsonlRun struct {
	proc       *procx.Proc
	events     chan Event
	done       chan struct{}
	stopEmit   chan struct{}
	stopOnce   sync.Once
	keepStdin  bool
	formatSend func(string) ([]byte, error)
	stdinMu    sync.Mutex
	stdin      io.WriteCloser

	mu        sync.Mutex
	sessionID string
	final     Final
}

func startJSONL(ctx context.Context, cfg jsonlConfig) (*jsonlRun, error) {
	resolvedBin, err := exec.LookPath(cfg.Bin)
	if err != nil {
		return nil, &ProviderError{Class: NotInstalled, Msg: err.Error()}
	}

	cmd := exec.Command(resolvedBin, cfg.Args...)
	cmd.Dir = cfg.Dir
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ProviderError{Class: Crashed, Msg: fmt.Sprintf("stdout pipe: %v", err)}
	}

	stderrBuf := &stderrTailBuffer{limit: maxStderrTailBytes}
	cmd.Stderr = stderrBuf

	var stdinPipe io.WriteCloser
	if cfg.KeepStdin || len(cfg.StdinData) > 0 {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return nil, &ProviderError{Class: Crashed, Msg: fmt.Sprintf("stdin pipe: %v", err)}
		}
	}

	proc, err := procx.Start(cmd)
	if err != nil {
		if stdinPipe != nil {
			_ = stdinPipe.Close()
		}
		return nil, &ProviderError{Class: Crashed, Msg: err.Error()}
	}

	r := &jsonlRun{
		proc:       proc,
		events:     make(chan Event, eventBufferSize),
		done:       make(chan struct{}),
		stopEmit:   make(chan struct{}),
		keepStdin:  cfg.KeepStdin,
		formatSend: cfg.FormatSend,
		sessionID:  cfg.InitialSID,
	}
	if cfg.KeepStdin {
		r.stdin = stdinPipe
	} else if stdinPipe != nil {
		data := cfg.StdinData
		go func() {
			_, _ = stdinPipe.Write(data)
			_ = stdinPipe.Close()
		}()
	}

	// Terminate the child tree if the caller cancels ctx or hits a deadline.
	watchStop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Kill()
		case <-watchStop:
		}
	}()

	go r.pump(ctx, stdout, stderrBuf, cfg.ParseLine, cfg.InitialSID, watchStop)
	return r, nil
}

func (r *jsonlRun) pump(
	ctx context.Context,
	stdout io.ReadCloser,
	stderrBuf *stderrTailBuffer,
	parseLine func([]byte, *jsonlState) []Event,
	initialSID string,
	watchStop chan struct{},
) {
	defer close(r.done)
	defer close(r.events)
	defer close(watchStop)

	state := jsonlState{SessionID: initialSID}
	reader := bufio.NewReader(stdout)

	for {
		line, err := readLineLimited(reader, jsonLineLimit)
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			state.SawLine = true

			rawCopy := make([]byte, len(trimmed))
			copy(rawCopy, trimmed)
			r.emit(Event{Kind: EventRaw, Raw: rawCopy})

			if parseLine != nil && len(trimmed) > 0 {
				events := parseLine(trimmed, &state)
				if state.SessionID != "" {
					r.mu.Lock()
					r.sessionID = state.SessionID
					r.mu.Unlock()
				}
				for _, ev := range events {
					r.emit(ev)
				}
			}
		}
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				state.FinalErr = &ProviderError{Class: Crashed, Msg: err.Error()}
				_ = r.Kill()
			}
			break
		}
	}

	waitErr := r.proc.Wait()
	r.closeStdin()

	final := Final{
		Text:      state.FinalText,
		SessionID: state.SessionID,
	}

	code := procExitCode(waitErr)
	stderrStr := strings.TrimSpace(stderrBuf.String())

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		final.Err = &ProviderError{Class: Timeout, Msg: "tiempo de espera agotado"}
	case state.FinalErr != nil:
		final.Err = state.FinalErr
	case !state.SawLine:
		if code != 0 && stderrStr != "" {
			final.Err = &ProviderError{Class: Classify(stderrStr), Msg: stderrStr}
		} else {
			final.Err = &ProviderError{Class: EmptyOutput, Msg: "el CLI terminó sin salida"}
		}
	case code != 0:
		msg := stderrStr
		if msg == "" {
			msg = fmt.Sprintf("el CLI salió con código %d", code)
		}
		final.Err = &ProviderError{Class: Classify(stderrStr), Msg: msg}
	}

	r.mu.Lock()
	r.final = final
	r.mu.Unlock()
}

func (r *jsonlRun) emit(ev Event) {
	select {
	case r.events <- ev:
	case <-r.stopEmit:
	}
}

func (r *jsonlRun) Events() <-chan Event {
	return r.events
}

func (r *jsonlRun) Send(text string) error {
	if !r.keepStdin || r.formatSend == nil {
		return errors.New("provider: Send solo está permitido en modo Orchestrator")
	}
	payload, err := r.formatSend(text)
	if err != nil {
		return err
	}

	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if r.stdin == nil {
		return errors.New("provider: entrada estándar cerrada")
	}
	if _, err := r.stdin.Write(payload); err != nil {
		return fmt.Errorf("provider: escribiendo en stdin: %w", err)
	}
	return nil
}

func (r *jsonlRun) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

func (r *jsonlRun) Wait() Final {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.final
}

func (r *jsonlRun) Kill() error {
	r.stopOnce.Do(func() {
		close(r.stopEmit)
	})
	r.closeStdin()
	return r.proc.Kill()
}

func (r *jsonlRun) closeStdin() {
	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if r.stdin != nil {
		_ = r.stdin.Close()
		r.stdin = nil
	}
}

func procExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type stderrTailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *stderrTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	if n >= b.limit {
		b.data = append(b.data[:0], p[n-b.limit:]...)
		return n, nil
	}
	if len(b.data)+n > b.limit {
		drop := len(b.data) + n - b.limit
		copy(b.data, b.data[drop:])
		b.data = b.data[:len(b.data)-drop]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (b *stderrTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
