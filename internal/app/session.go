package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/mcpserver"
	"github.com/ImKrav/weaverharness/internal/prompts"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

// OrchestratorTools is the fixed tool allowlist passed to the Claude orchestrator in v1.
var OrchestratorTools = []string{
	"Read",
	"Grep",
	"Glob",
	"Edit(./docs/**)",
	"Write(./docs/**)",
	"Bash(git *)",
	"mcp__weaver",
}

// SessionOptions configures a RunSession invocation.
type SessionOptions struct {
	Dir      string
	ResumeID string
	Resume   bool
	In       io.Reader
	Out      io.Writer
	Headless func(send func(string) error, events <-chan provider.Event) error
}

type tuiBridge struct {
	mu sync.RWMutex
	p  *tea.Program
}

func (b *tuiBridge) setProgram(p *tea.Program) {
	b.mu.Lock()
	b.p = p
	b.mu.Unlock()
}

func (b *tuiBridge) Ask(ctx context.Context, r engine.ApprovalRequest) (bool, string, error) {
	b.mu.RLock()
	p := b.p
	b.mu.RUnlock()
	if p == nil {
		return false, "", errors.New("tui: programa no activo")
	}
	return tui.Approver{P: p}.Ask(ctx, r)
}

func (b *tuiBridge) Notify(v any) {
	b.mu.RLock()
	p := b.p
	b.mu.RUnlock()
	if p == nil {
		return
	}
	switch msg := v.(type) {
	case []engine.AgentInfo:
		p.Send(tui.AgentsChanged{Agents: msg})
	case []gitx.Violation:
		p.Send(tui.RefViolations{Items: msg})
	}
}

type rejectApprover struct{}

func (rejectApprover) Ask(context.Context, engine.ApprovalRequest) (bool, string, error) {
	return false, "modo headless sin aprobador", nil
}

// RunSession runs an interactive TUI (or Headless test) orchestration session in dir.
func RunSession(ctx context.Context, o SessionOptions) error {
	dir := o.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("app: leyendo directorio actual: %w", err)
		}
	}
	in := o.In
	if in == nil {
		in = os.Stdin
	}
	out := o.Out
	if out == nil {
		out = os.Stdout
	}

	repo, err := gitx.Open(dir)
	if err != nil {
		return err
	}

	dirty, err := repo.IsDirty(ctx)
	if err != nil {
		return fmt.Errorf("app: comprobando estado git: %w", err)
	}
	if dirty {
		fmt.Fprint(out, "Hay cambios sin commit; no entrarán en la ejecución. ¿Continuar? [s/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans != "s" && ans != "si" && ans != "sí" && ans != "y" && ans != "yes" {
			return nil
		}
	}

	if err := ensureWeaverExcluded(ctx, repo); err != nil {
		return err
	}

	globalPath, _ := config.GlobalPath()
	projectPath := filepath.Join(repo.Dir, ".weaver", "config.toml")
	cfg, err := config.Load(globalPath, projectPath)
	if err != nil {
		return err
	}

	providers := make(map[string]provider.Provider, len(config.ProviderNames))
	for _, name := range config.ProviderNames {
		p, err := provider.New(name, cfg.Providers[name].Bin)
		if err != nil {
			return err
		}
		providers[name] = p
	}

	store := run.Store{Root: filepath.Join(repo.Dir, ".weaver", "runs")}

	var (
		bridge   *tuiBridge
		approver engine.Approver
		notify   func(any)
	)
	if o.Headless == nil {
		bridge = &tuiBridge{}
		approver = bridge
		notify = bridge.Notify
	} else {
		approver = rejectApprover{}
	}

	eng, err := engine.New(ctx, engine.Options{
		Cfg:       cfg,
		Repo:      repo,
		Store:     store,
		Providers: providers,
		Approver:  approver,
		Notify:    notify,
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = eng.Close(context.Background())
	}()

	var st *run.State
	if o.Resume {
		st, err = eng.Resume(ctx, o.ResumeID)
	} else {
		st, err = eng.StartRun(ctx)
	}
	if err != nil {
		return err
	}

	mcpCtx, mcpCancel := context.WithCancel(ctx)
	defer mcpCancel()

	mcpURL, mcpToken, err := mcpserver.Listen(mcpCtx, mcpserver.NewHandler(eng))
	if err != nil {
		return err
	}

	runDir := filepath.Join(store.Root, st.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("app: creando directorio de ejecución: %w", err)
	}
	mcpCfgPath := filepath.Join(runDir, "mcp.json")
	if err := mcpserver.WriteMCPConfig(mcpCfgPath, mcpURL, mcpToken); err != nil {
		return err
	}

	orchPromptPath := filepath.Join(runDir, "orchestrator.md")
	if err := os.WriteFile(orchPromptPath, []byte(prompts.Orchestrator(repo.Dir)), 0o600); err != nil {
		return fmt.Errorf("app: escribiendo prompt del orquestador: %w", err)
	}

	orchRole := cfg.Roles[cfg.Orchestrator]
	orchProv := providers[orchRole.Provider]
	req := provider.RunRequest{
		Mode:             provider.Orchestrator,
		Dir:              st.SessionDir,
		Model:            orchRole.Model,
		SystemPromptFile: orchPromptPath,
		MCPConfigFile:    mcpCfgPath,
		AllowedTools:     OrchestratorTools,
	}
	if o.Resume && st.OrchestratorSessionID != "" {
		req.ResumeID = st.OrchestratorSessionID
	} else {
		sid, err := newUUIDv4()
		if err != nil {
			return err
		}
		req.SessionID = sid
		if err := eng.SetOrchestratorSessionID(sid); err != nil {
			return err
		}
	}

	orchRun, err := orchProv.Start(ctx, req)
	if err != nil {
		return fmt.Errorf("app: iniciando orquestador: %w", err)
	}
	defer func() {
		_ = orchRun.Kill()
		for range orchRun.Events() {
		}
		_ = orchRun.Wait()
	}()

	if o.Headless != nil {
		return runHeadless(ctx, eng, orchRun, o.Headless)
	}

	title := fmt.Sprintf("WeaverHarness · run %s · orquestador %s/%s", st.RunID, orchRole.Provider, orchRole.Model)
	detailFn := func(agentID string) tui.AgentDetail {
		cur := eng.State()
		if cur == nil {
			return tui.AgentDetail{ID: agentID}
		}
		agDir := store.AgentDir(cur.RunID, agentID)
		return tui.AgentDetail{
			ID:           agentID,
			Receipt:      readTextFile(filepath.Join(agDir, "receipt.json")),
			Verification: readTextFile(filepath.Join(agDir, "verification.json")),
			LogTail:      readTailLines(filepath.Join(agDir, "transcript.jsonl"), 80),
		}
	}

	model := tui.New(title, orchRun.Send, detailFn)
	p := tea.NewProgram(model, tea.WithAltScreen())
	bridge.setProgram(p)

	if existing := eng.Status(""); len(existing) > 0 {
		p.Send(tui.AgentsChanged{Agents: existing})
	}

	fwdDone := make(chan struct{})
	go func() {
		defer close(fwdDone)
		for ev := range orchRun.Events() {
			p.Send(tui.OrchestratorEvent{Event: ev})
			if ev.Kind == provider.EventTurnEnd {
				_ = eng.CheckRefs(ctx)
			}
		}
		final := orchRun.Wait()
		p.Send(tui.OrchestratorExited{Err: final.Err})
	}()

	_, runErr := p.Run()
	_ = orchRun.Kill()
	<-fwdDone
	return runErr
}

func runHeadless(
	ctx context.Context,
	eng *engine.Engine,
	orchRun provider.Run,
	fn func(send func(string) error, events <-chan provider.Event) error,
) error {
	fwd := make(chan provider.Event, 64)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(fwd)
		for ev := range orchRun.Events() {
			if ev.Kind == provider.EventTurnEnd {
				_ = eng.CheckRefs(ctx)
			}
			select {
			case fwd <- ev:
			case <-stop:
				return
			}
		}
	}()

	err := fn(orchRun.Send, fwd)
	close(stop)
	_ = orchRun.Kill()
	<-done
	return err
}

func ensureWeaverExcluded(ctx context.Context, repo gitx.Repo) error {
	commonDir, err := repo.CommonDir(ctx)
	if err != nil {
		return fmt.Errorf("app: resolviendo CommonDir: %w", err)
	}
	infoDir := filepath.Join(commonDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return fmt.Errorf("app: creando %s: %w", infoDir, err)
	}
	excludePath := filepath.Join(infoDir, "exclude")

	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("app: leyendo %s: %w", excludePath, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == ".weaver/" || trimmed == ".weaver" {
			return nil
		}
	}

	var updated strings.Builder
	updated.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		updated.WriteByte('\n')
	}
	updated.WriteString(".weaver/\n")
	if err := os.WriteFile(excludePath, []byte(updated.String()), 0o644); err != nil {
		return fmt.Errorf("app: escribiendo %s: %w", excludePath, err)
	}
	return nil
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("app: generando uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func readTextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readTailLines(path string, maxLines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
