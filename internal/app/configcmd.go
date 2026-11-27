package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
)

// ConfigShowOptions configures app.ConfigShow.
type ConfigShowOptions struct {
	Dir    string
	Preset string
	Out    io.Writer
}

// ConfigInitOptions configures app.ConfigInit.
type ConfigInitOptions struct {
	Dir    string
	Preset string
	Global bool
	Force  bool
	Out    io.Writer
}

func resolveConfigRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("config: leyendo directorio actual: %w", err)
		}
	}
	if repo, err := gitx.Open(dir); err == nil {
		return repo.Dir, nil
	}
	return dir, nil
}

// ConfigShow prints the effective merged configuration as TOML, optionally overlaid with a preset.
func ConfigShow(o ConfigShowOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	rootDir, err := resolveConfigRoot(o.Dir)
	if err != nil {
		return err
	}

	globalPath, _ := config.GlobalPath()
	projectPath := filepath.Join(rootDir, ".weaver", "config.toml")
	cfg, err := config.Load(globalPath, projectPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Preset) != "" {
		cfg, err = config.ApplyPreset(cfg, o.Preset)
		if err != nil {
			return err
		}
	}

	rendered, err := config.RenderTOML(cfg)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, rendered)
	return err
}

// ConfigInit writes an initial config.toml file for the project or user global directory.
func ConfigInit(o ConfigInitOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}

	var targetPath string
	if o.Global {
		p, err := config.GlobalPath()
		if err != nil {
			return err
		}
		targetPath = p
	} else {
		rootDir, err := resolveConfigRoot(o.Dir)
		if err != nil {
			return err
		}
		targetPath = filepath.Join(rootDir, ".weaver", "config.toml")
	}

	if _, err := os.Stat(targetPath); err == nil && !o.Force {
		return fmt.Errorf("config init: el archivo %s ya existe (usa --force para sobrescribir)", targetPath)
	}

	cfg := config.Default()
	preset := strings.TrimSpace(o.Preset)
	if preset == "" {
		preset = "default"
	}
	cfg, err := config.ApplyPreset(cfg, preset)
	if err != nil {
		return err
	}

	rendered, err := config.RenderTOML(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("config init: creando directorio: %w", err)
	}
	if err := os.WriteFile(targetPath, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("config init: escribiendo %s: %w", targetPath, err)
	}
	fmt.Fprintf(out, "Configuración inicializada en %s (preset: %s)\n", targetPath, preset)
	return nil
}
