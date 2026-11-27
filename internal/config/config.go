package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

// ProviderNames lists the supported provider identifiers in v1.
var ProviderNames = []string{"claude", "codex", "antigravity", "opencode"}

// Duration wraps time.Duration for TOML text unmarshaling.
type Duration struct {
	time.Duration
}

// UnmarshalText parses a Go duration string such as "10m" or "30s".
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("duración inválida %q: %w", string(text), err)
	}
	d.Duration = parsed
	return nil
}

// MarshalText formats the duration as a Go duration string.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// Role maps a logical role name to a provider and model.
type Role struct {
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
}

// ProviderCfg holds per-provider executable and concurrency settings.
type ProviderCfg struct {
	Bin         string `toml:"bin"`
	MaxParallel int    `toml:"max_parallel"`
}

// Config holds the resolved WeaverHarness configuration.
type Config struct {
	Orchestrator string                 `toml:"orchestrator"`
	ReviewRounds int                    `toml:"review_rounds"`
	CheckTimeout Duration               `toml:"check_timeout"`
	TaskTimeout  Duration               `toml:"task_timeout"`
	Roles        map[string]Role        `toml:"roles"`
	Providers    map[string]ProviderCfg `toml:"providers"`
}

// Preset defines a named role assignment profile.
type Preset struct {
	Name        string
	Description string
	Roles       map[string]Role
}

var builtinPresets = []Preset{
	{
		Name:        "default",
		Description: "architect=claude/opus, implementer=antigravity/gemini-4-hydrogen, reviewer=codex/gpt-5.6-sol",
		Roles: map[string]Role{
			"architect":   {Provider: "claude", Model: "opus"},
			"implementer": {Provider: "antigravity", Model: "gemini-4-hydrogen"},
			"reviewer":    {Provider: "codex", Model: "gpt-5.6-sol"},
		},
	},
	{
		Name:        "claude-codex",
		Description: "architect=claude/opus, implementer=codex/gpt-5.6-sol, reviewer=codex/gpt-5.6-sol",
		Roles: map[string]Role{
			"architect":   {Provider: "claude", Model: "opus"},
			"implementer": {Provider: "codex", Model: "gpt-5.6-sol"},
			"reviewer":    {Provider: "codex", Model: "gpt-5.6-sol"},
		},
	},
	{
		Name:        "claude-opencode",
		Description: "architect=claude/opus, implementer=opencode/opencode/big-pickle, reviewer=codex/gpt-5.6-sol",
		Roles: map[string]Role{
			"architect":   {Provider: "claude", Model: "opus"},
			"implementer": {Provider: "opencode", Model: "opencode/big-pickle"},
			"reviewer":    {Provider: "codex", Model: "gpt-5.6-sol"},
		},
	},
	{
		Name:        "claude-only",
		Description: "architect=claude/opus, implementer=claude/sonnet, reviewer=claude/sonnet",
		Roles: map[string]Role{
			"architect":   {Provider: "claude", Model: "opus"},
			"implementer": {Provider: "claude", Model: "sonnet"},
			"reviewer":    {Provider: "claude", Model: "sonnet"},
		},
	},
}

// Presets returns all built-in role configuration presets.
func Presets() []Preset {
	out := make([]Preset, len(builtinPresets))
	for i, p := range builtinPresets {
		out[i] = Preset{
			Name:        p.Name,
			Description: p.Description,
			Roles:       maps.Clone(p.Roles),
		}
	}
	return out
}

// ApplyPreset overrides cfg.Roles according to the named preset and validates the result.
func ApplyPreset(cfg Config, name string) (Config, error) {
	target := strings.TrimSpace(name)
	var found *Preset
	names := make([]string, 0, len(builtinPresets))
	for i := range builtinPresets {
		names = append(names, builtinPresets[i].Name)
		if builtinPresets[i].Name == target {
			found = &builtinPresets[i]
		}
	}
	if found == nil {
		return Config{}, fmt.Errorf("config: preset desconocido %q (válidos: %s)", name, strings.Join(names, ", "))
	}

	next := cfg
	next.Roles = maps.Clone(cfg.Roles)
	if next.Roles == nil {
		next.Roles = make(map[string]Role, len(found.Roles))
	}
	for roleName, role := range found.Roles {
		next.Roles[roleName] = role
	}
	if cfg.Providers != nil {
		next.Providers = maps.Clone(cfg.Providers)
	}
	if err := validate(&next); err != nil {
		return Config{}, err
	}
	return next, nil
}

// RenderTOML serializes cfg as a valid TOML document.
func RenderTOML(cfg Config) (string, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return "", fmt.Errorf("config: serializando TOML: %w", err)
	}
	return buf.String(), nil
}

// Default returns the embedded default configuration.
func Default() Config {
	var cfg Config
	if _, err := toml.Decode(defaultTOML, &cfg); err != nil {
		panic(fmt.Sprintf("config: default.toml inválido: %v", err))
	}
	return cfg
}

// GlobalPath returns the per-user configuration file path.
func GlobalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: directorio de usuario: %w", err)
	}
	return filepath.Join(dir, "weaver", "config.toml"), nil
}

// Load layers default, global, and project configuration files and validates the result.
func Load(globalPath, projectPath string) (Config, error) {
	cfg := Default()

	for _, p := range []string{globalPath, projectPath} {
		if p == "" {
			continue
		}
		if _, err := toml.DecodeFile(p, &cfg); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return Config{}, fmt.Errorf("config: leyendo %s: %w", p, err)
		}
	}

	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	for roleName, role := range cfg.Roles {
		if !slices.Contains(ProviderNames, role.Provider) {
			return fmt.Errorf("config: rol %q usa proveedor desconocido %q", roleName, role.Provider)
		}
	}

	for provName, provCfg := range cfg.Providers {
		if !slices.Contains(ProviderNames, provName) {
			return fmt.Errorf("config: proveedor desconocido %q", provName)
		}
		if provCfg.MaxParallel == 0 {
			provCfg.MaxParallel = 3
			cfg.Providers[provName] = provCfg
		} else if provCfg.MaxParallel < 0 {
			return fmt.Errorf("config: max_parallel inválido (%d) para %q", provCfg.MaxParallel, provName)
		}
	}

	orchRole, ok := cfg.Roles[cfg.Orchestrator]
	if !ok {
		return fmt.Errorf("config: rol orquestador %q no existe", cfg.Orchestrator)
	}
	if orchRole.Provider != "claude" {
		return errors.New("v1: solo claude puede ser orquestador")
	}
	if cfg.CheckTimeout.Duration <= 0 {
		return errors.New("config: check_timeout debe ser positivo")
	}
	if cfg.TaskTimeout.Duration <= 0 {
		return errors.New("config: task_timeout debe ser positivo")
	}
	if cfg.ReviewRounds < 0 {
		return errors.New("config: review_rounds no puede ser negativo")
	}

	return nil
}
