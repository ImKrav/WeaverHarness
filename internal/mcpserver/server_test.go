package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerRT struct {
	token string
	base  http.RoundTripper
}

func (b bearerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

type alwaysApprove struct{}

func (alwaysApprove) Ask(context.Context, engine.ApprovalRequest) (bool, string, error) {
	return true, "aprobado", nil
}

type stubRun struct {
	events chan provider.Event
	final  provider.Final
}

func (r *stubRun) Events() <-chan provider.Event { return r.events }
func (r *stubRun) Send(string) error           { return nil }
func (r *stubRun) SessionID() string           { return r.final.SessionID }
func (r *stubRun) Wait() provider.Final        { return r.final }
func (r *stubRun) Kill() error                 { return nil }

type stubProvider struct {
	name    string
	handler func(context.Context, provider.RunRequest) (provider.Run, error)
}

func (p *stubProvider) Name() string                       { return p.name }
func (p *stubProvider) Check(context.Context) provider.Health { return provider.Health{Installed: true, LoggedIn: true} }
func (p *stubProvider) Start(ctx context.Context, req provider.RunRequest) (provider.Run, error) {
	return p.handler(ctx, req)
}

func newTestEngine(t *testing.T, prov provider.Provider) *engine.Engine {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "mcp repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Weaver Test")
	runGit("config", "user.email", "weaver@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	repo, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(gitx.WeaverDir(repo.Dir))
	})

	cfg := config.Default()
	cfg.CheckTimeout = config.Duration{Duration: 5 * time.Second}
	cfg.TaskTimeout = config.Duration{Duration: 5 * time.Second}
	cfg.Roles["implementer"] = config.Role{Provider: "codex", Model: "m1"}

	if prov == nil {
		prov = &stubProvider{name: "codex"}
	}
	eng, err := engine.New(ctx, engine.Options{
		Cfg:       cfg,
		Repo:      repo,
		Store:     run.Store{Root: filepath.Join(repo.Dir, ".weaver", "runs")},
		Providers: map[string]provider.Provider{"codex": prov},
		Approver:  alwaysApprove{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = eng.Close(context.Background())
	})
	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}
	return eng
}

func connectTestClient(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	tr := &mcp.StreamableClientTransport{
		Endpoint: url,
		HTTPClient: &http.Client{
			Transport: bearerRT{token: token, base: http.DefaultTransport},
		},
	}
	sess, err := client.Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatalf("client.Connect() error: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
	})
	return sess
}

func TestRejectsWithoutToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := newTestEngine(t, nil)
	url, token, err := Listen(ctx, NewHandler(eng))
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token len = %d, want 64 hex chars (32 bytes)", len(token))
	}

	resp, err := http.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("http.Post() error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	// Verify WriteMCPConfig format.
	cfgPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := WriteMCPConfig(cfgPath, url, token); err != nil {
		t.Fatalf("WriteMCPConfig() error: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	w, ok := parsed.MCPServers["weaver"]
	if !ok || w.Type != "http" || w.URL != url || w.Headers["Authorization"] != "Bearer "+token {
		t.Fatalf("unexpected mcp.json: %s", string(raw))
	}
}

func TestToolsListed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := newTestEngine(t, nil)
	url, token, err := Listen(ctx, NewHandler(eng))
	if err != nil {
		t.Fatal(err)
	}
	sess := connectTestClient(t, url, token)

	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	want := []string{
		"accept_task",
		"agent_status",
		"cancel_agent",
		"cleanup_agent",
		"follow_up",
		"request_approval",
		"spawn_agent",
		"wait_agents",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestSpawnBeforePlanIsToolError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := newTestEngine(t, nil)
	url, token, err := Listen(ctx, NewHandler(eng))
	if err != nil {
		t.Fatal(err)
	}
	sess := connectTestClient(t, url, token)

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "spawn_agent",
		Arguments: map[string]any{
			"task_id":      "T1",
			"instructions": "haz algo",
		},
	})
	if err != nil {
		t.Fatalf("CallTool() returned protocol error %v, want tool result with IsError=true", err)
	}
	if !res.IsError {
		t.Fatalf("CallTool() IsError = false, want true")
	}
}

func TestWaitAgentsNoTranscript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prov := &stubProvider{
		name: "codex",
		handler: func(ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			ch := make(chan provider.Event, 2)
			ch <- provider.Event{Kind: provider.EventRaw, Raw: []byte(`{"line":"TRANSCRIPT-ONLY-SECRET"}`)}
			close(ch)

			receiptJSON, _ := json.Marshal(map[string]any{
				"task_id":      "T1",
				"status":       "done",
				"summary":      "listo",
				"spec_refs":    []string{"REQ-1"},
				"changes":      []map[string]string{},
				"commits":      []string{},
				"verification": []map[string]any{{"cmd": "echo ok", "exit_code": 0, "output_tail": "ok"}},
				"open_issues":  []string{},
			})
			return &stubRun{
				events: ch,
				final: provider.Final{
					SessionID: "sid-1",
					Text:      "```weaver-receipt\n" + string(receiptJSON) + "\n```",
				},
			}, nil
		},
	}

	eng := newTestEngine(t, prov)
	url, token, err := Listen(ctx, NewHandler(eng))
	if err != nil {
		t.Fatal(err)
	}
	sess := connectTestClient(t, url, token)

	if res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_approval",
		Arguments: map[string]any{"kind": "spec", "path": "docs/spec.md", "summary": "spec"},
	}); err != nil || res.IsError {
		t.Fatalf("spec approval failed: err=%v res=%+v", err, res)
	}

	if res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "request_approval",
		Arguments: map[string]any{
			"kind":    "plan",
			"path":    "docs/plan.md",
			"summary": "plan",
			"tasks": []map[string]any{
				{
					"id":         "T1",
					"title":      "Primera",
					"role":       "implementer",
					"spec_refs":  []string{"REQ-1"},
					"depends_on": []string{},
					"checks":     []string{"echo ok"},
				},
			},
			"global_checks": []string{"echo ok"},
		},
	}); err != nil || res.IsError {
		t.Fatalf("plan approval failed: err=%v res=%+v", err, res)
	}

	spawnRes, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "spawn_agent",
		Arguments: map[string]any{
			"task_id":      "T1",
			"instructions": "haz T1",
		},
	})
	if err != nil || spawnRes.IsError {
		t.Fatalf("spawn failed: err=%v res=%+v", err, spawnRes)
	}
	var spawnOut struct {
		AgentID string `json:"agent_id"`
	}
	if tc, ok := spawnRes.Content[0].(*mcp.TextContent); !ok || json.Unmarshal([]byte(tc.Text), &spawnOut) != nil {
		t.Fatalf("unexpected spawn content: %+v", spawnRes.Content)
	}

	waitRes, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "wait_agents",
		Arguments: map[string]any{
			"agent_ids": []string{spawnOut.AgentID},
			"timeout_s": 10,
		},
	})
	if err != nil || waitRes.IsError {
		t.Fatalf("wait_agents failed: err=%v res=%+v", err, waitRes)
	}

	var combined strings.Builder
	for _, c := range waitRes.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			combined.WriteString(tc.Text)
		}
	}
	if strings.Contains(combined.String(), "TRANSCRIPT-ONLY") {
		t.Fatalf("wait_agents leaked transcript content: %s", combined.String())
	}
	if !strings.Contains(combined.String(), "listo") {
		t.Fatalf("wait_agents missing receipt summary: %s", combined.String())
	}
}
