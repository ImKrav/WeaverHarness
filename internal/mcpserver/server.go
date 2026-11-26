package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewHandler creates an HTTP handler exposing the WeaverHarness orchestrator MCP tools.
func NewHandler(e *engine.Engine) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "weaver", Version: "1.0.0"}, nil)
	registerTools(srv, e)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, nil)
}

// Listen binds 127.0.0.1 on an ephemeral port, enforces Bearer token auth, and serves h until ctx cancels.
func Listen(ctx context.Context, h http.Handler) (url, token string, err error) {
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", "", fmt.Errorf("mcpserver: generando token: %w", err)
	}
	token = hex.EncodeToString(rawToken)
	expectedAuth := []byte("Bearer " + token)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("mcpserver: abriendo puerto local: %w", err)
	}

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, expectedAuth) != 1 {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})

	srv := &http.Server{Handler: authed}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			_ = serveErr
		}
	}()
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	return "http://" + ln.Addr().String(), token, nil
}

// WriteMCPConfig writes a Claude --mcp-config JSON file pointing to the local Weaver MCP server.
func WriteMCPConfig(path, url, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mcpserver: creando directorio para mcp config: %w", err)
	}
	payload := map[string]any{
		"mcpServers": map[string]any{
			"weaver": map[string]any{
				"type": "http",
				"url":  url,
				"headers": map[string]string{
					"Authorization": "Bearer " + token,
				},
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("mcpserver: serializando mcp config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("mcpserver: escribiendo %s: %w", path, err)
	}
	return nil
}
