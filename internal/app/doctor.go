package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
)

// Doctor checks provider installations, login status, and configured role models, returning exit code 0 or 1.
func Doctor(ctx context.Context, dir string, out io.Writer) int {
	if out == nil {
		out = io.Discard
	}

	projectDir := dir
	if repo, err := gitx.Open(dir); err == nil {
		projectDir = repo.Dir
	}
	globalPath, _ := config.GlobalPath()
	var projectPath string
	if projectDir != "" {
		projectPath = filepath.Join(projectDir, ".weaver", "config.toml")
	}

	cfg, err := config.Load(globalPath, projectPath)
	if err != nil {
		fmt.Fprintf(out, "Error cargando configuración: %v\n", err)
		return 1
	}

	healths := make(map[string]provider.Health, len(config.ProviderNames))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, name := range config.ProviderNames {
		p, err := provider.New(name, cfg.Providers[name].Bin)
		if err != nil {
			healths[name] = provider.Health{Detail: err.Error()}
			continue
		}
		wg.Add(1)
		go func(name string, p provider.Provider) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			h := p.Check(checkCtx)
			mu.Lock()
			healths[name] = h
			mu.Unlock()
		}(name, p)
	}
	wg.Wait()

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVEEDOR\tINSTALADO\tVERSIÓN\tSESIÓN\tNOTAS")

	for _, name := range config.ProviderNames {
		h := healths[name]
		installed := "no"
		if h.Installed {
			installed = "sí"
		}
		logged := "no"
		if h.LoggedIn {
			logged = "sí"
		}
		version := h.Version
		if version == "" {
			version = "-"
		}

		var notes []string
		if name == "antigravity" {
			notes = append(notes, "soporte de términos no confirmado")
		}
		if len(h.Models) > 0 {
			for roleName, role := range cfg.Roles {
				if role.Provider == name && role.Model != "" && !slices.Contains(h.Models, role.Model) {
					notes = append(notes, fmt.Sprintf("rol %s: modelo %q no aparece en el CLI", roleName, role.Model))
				}
			}
		}
		if (!h.Installed || !h.LoggedIn) && h.Detail != "" {
			singleLine := strings.Join(strings.Fields(h.Detail), " ")
			if len(singleLine) > 80 {
				singleLine = singleLine[:77] + "..."
			}
			notes = append(notes, singleLine)
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, installed, version, logged, strings.Join(notes, "; "))
	}
	_ = tw.Flush()

	orchRole := cfg.Roles[cfg.Orchestrator]
	orchHealth := healths[orchRole.Provider]
	if !orchHealth.Installed || !orchHealth.LoggedIn {
		return 1
	}
	if len(orchHealth.Models) > 0 && orchRole.Model != "" && !slices.Contains(orchHealth.Models, orchRole.Model) {
		return 1
	}
	return 0
}
