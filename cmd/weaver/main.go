package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/ImKrav/weaverharness/internal/app"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type cliDeps struct {
	getwd  func() (string, error)
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `Uso: weaver [opciones] [comando]

Comandos:
  (sin argumentos)                    Inicia una sesión interactiva (TUI) en el repositorio actual
  resume [--preset N] [--skip-check] [--no-mouse] [run-id]
                                      Reanuda la última ejecución o la indicada por run-id
  runs                                Lista las ejecuciones guardadas en .weaver/runs
  inspect [--json] [run-id]           Muestra estado, tareas, agentes y recibos de una ejecución
  logs [--tail N] [--only-receipt] [run-id] <agent-id>
                                      Muestra recibo, verificación y últimas líneas del transcript
  clean [--dry-run] [--all] [--force] Limpia worktrees, ramas weaver/* y runs finalizados
  config <show|init> [opciones]       Muestra o inicializa .weaver/config.toml (con --preset)
  doctor                              Comprueba instalación, sesión y modelos de los CLIs proveedores
  version                             Muestra la versión compilada de weaver

Opciones globales / de sesión:
  --preset <nombre>                   Aplica un preset (default, claude-codex, claude-opencode, claude-only)
  --skip-check                        Omite la verificación previa del CLI orquestador al arrancar
  --no-mouse                          Desactiva captura de ratón en la TUI para facilitar copiar/pegar
  -v, --version                       Muestra la versión compilada de weaver
  -h, --help                          Muestra esta ayuda`)
}

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	return runMainWith(args, cliDeps{
		getwd:  os.Getwd,
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
	})
}

func runMainWith(args []string, deps cliDeps) int {
	if deps.getwd == nil {
		deps.getwd = os.Getwd
	}
	if deps.stdin == nil {
		deps.stdin = os.Stdin
	}
	if deps.stdout == nil {
		deps.stdout = os.Stdout
	}
	if deps.stderr == nil {
		deps.stderr = os.Stderr
	}

	fs := flag.NewFlagSet("weaver", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	var showVersion, skipCheck, noMouse bool
	var preset string
	fs.BoolVar(&showVersion, "v", false, "Muestra la versión compilada")
	fs.BoolVar(&showVersion, "version", false, "Muestra la versión compilada")
	fs.StringVar(&preset, "preset", "", "Aplica un preset de roles")
	fs.BoolVar(&skipCheck, "skip-check", false, "Omite el pre-check de arranque")
	fs.BoolVar(&noMouse, "no-mouse", false, "Desactiva captura de ratón en la TUI")
	fs.Usage = func() { usage(deps.stderr) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	rest := fs.Args()
	if showVersion {
		if len(rest) > 0 {
			fmt.Fprintf(deps.stderr, "Argumentos inesperados con --version: %v\n", rest)
			return 2
		}
		fmt.Fprintf(deps.stdout, "weaver %s (%s, %s)\n", version, commit, date)
		return 0
	}

	if len(rest) > 0 && rest[0] == "version" {
		if len(rest) > 1 {
			fmt.Fprintf(deps.stderr, "Argumentos inesperados para 'version': %v\n", rest[1:])
			return 2
		}
		fmt.Fprintf(deps.stdout, "weaver %s (%s, %s)\n", version, commit, date)
		return 0
	}

	cwd, err := deps.getwd()
	if err != nil {
		fmt.Fprintf(deps.stderr, "Error obteniendo directorio actual: %v\n", err)
		return 1
	}

	if len(rest) == 0 {
		if err := app.RunSession(context.Background(), app.SessionOptions{
			Dir:           cwd,
			Preset:        preset,
			SkipPreflight: skipCheck,
			NoMouse:       noMouse,
			In:            deps.stdin,
			Out:           deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	switch rest[0] {
	case "resume":
		sub := flag.NewFlagSet("resume", flag.ContinueOnError)
		sub.SetOutput(deps.stderr)
		subPreset := preset
		subSkipCheck := skipCheck
		subNoMouse := noMouse
		sub.StringVar(&subPreset, "preset", subPreset, "Aplica un preset de roles")
		sub.BoolVar(&subSkipCheck, "skip-check", subSkipCheck, "Omite el pre-check de arranque")
		sub.BoolVar(&subNoMouse, "no-mouse", subNoMouse, "Desactiva captura de ratón en la TUI")
		sub.Usage = func() {
			fmt.Fprintln(deps.stderr, "Uso: weaver resume [--preset <nombre>] [--skip-check] [--no-mouse] [run-id]")
		}
		if err := sub.Parse(rest[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if sub.NArg() > 1 {
			sub.Usage()
			return 2
		}
		var runID string
		if sub.NArg() == 1 {
			runID = sub.Arg(0)
		}
		if err := app.RunSession(context.Background(), app.SessionOptions{
			Dir:           cwd,
			Resume:        true,
			ResumeID:      runID,
			Preset:        subPreset,
			SkipPreflight: subSkipCheck,
			NoMouse:       subNoMouse,
			In:            deps.stdin,
			Out:           deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error al reanudar: %v\n", err)
			return 1
		}
		return 0

	case "runs":
		if len(rest) > 1 {
			fmt.Fprintf(deps.stderr, "Uso: weaver runs\n")
			return 2
		}
		if err := app.Runs(cwd, deps.stdout); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case "inspect":
		sub := flag.NewFlagSet("inspect", flag.ContinueOnError)
		sub.SetOutput(deps.stderr)
		var asJSON bool
		sub.BoolVar(&asJSON, "json", false, "Imprime la inspección en formato JSON")
		sub.Usage = func() {
			fmt.Fprintln(deps.stderr, "Uso: weaver inspect [--json] [run-id]")
		}
		if err := sub.Parse(rest[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if sub.NArg() > 1 {
			sub.Usage()
			return 2
		}
		var runID string
		if sub.NArg() == 1 {
			runID = sub.Arg(0)
		}
		if err := app.Inspect(app.InspectOptions{
			Dir:   cwd,
			RunID: runID,
			JSON:  asJSON,
			Out:   deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case "logs":
		sub := flag.NewFlagSet("logs", flag.ContinueOnError)
		sub.SetOutput(deps.stderr)
		var tail int
		var onlyReceipt bool
		sub.IntVar(&tail, "tail", 100, "Número de líneas finales del transcript")
		sub.BoolVar(&onlyReceipt, "only-receipt", false, "Omite transcript.jsonl y muestra solo recibo y verificación")
		sub.Usage = func() {
			fmt.Fprintln(deps.stderr, "Uso: weaver logs [--tail N] [--only-receipt] [run-id] <agent-id>")
		}
		if err := sub.Parse(rest[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		var runID, agentID string
		switch sub.NArg() {
		case 1:
			agentID = sub.Arg(0)
		case 2:
			runID = sub.Arg(0)
			agentID = sub.Arg(1)
		default:
			sub.Usage()
			return 2
		}
		if err := app.Logs(app.LogsOptions{
			Dir:         cwd,
			RunID:       runID,
			AgentID:     agentID,
			Tail:        tail,
			OnlyReceipt: onlyReceipt,
			Out:         deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case "clean":
		sub := flag.NewFlagSet("clean", flag.ContinueOnError)
		sub.SetOutput(deps.stderr)
		var dryRun, all, force bool
		sub.BoolVar(&dryRun, "dry-run", false, "Muestra lo que se limpiaría sin borrar nada")
		sub.BoolVar(&all, "all", false, "Incluye ejecuciones abiertas y todos los worktrees de weaver")
		sub.BoolVar(&force, "force", false, "Omite la confirmación interactiva al usar --all")
		sub.Usage = func() {
			fmt.Fprintln(deps.stderr, "Uso: weaver clean [--dry-run] [--all] [--force]")
		}
		if err := sub.Parse(rest[1:]); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if sub.NArg() > 0 {
			sub.Usage()
			return 2
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if err := app.Clean(ctx, app.CleanOptions{
			Dir:    cwd,
			DryRun: dryRun,
			All:    all,
			Force:  force,
			In:     deps.stdin,
			Out:    deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case "config":
		if len(rest) < 2 {
			fmt.Fprintln(deps.stderr, "Uso: weaver config <show|init> [opciones]")
			return 2
		}
		switch rest[1] {
		case "-h", "--help", "help":
			fmt.Fprintln(deps.stderr, `Uso:
  weaver config show [--preset <nombre>]
  weaver config init [--preset <nombre>] [--global] [--force]`)
			return 0
		case "show":
			sub := flag.NewFlagSet("config show", flag.ContinueOnError)
			sub.SetOutput(deps.stderr)
			subPreset := preset
			sub.StringVar(&subPreset, "preset", subPreset, "Muestra la configuración aplicando un preset")
			sub.Usage = func() {
				fmt.Fprintln(deps.stderr, "Uso: weaver config show [--preset <nombre>]")
			}
			if err := sub.Parse(rest[2:]); err != nil {
				if err == flag.ErrHelp {
					return 0
				}
				return 2
			}
			if sub.NArg() > 0 {
				sub.Usage()
				return 2
			}
			if err := app.ConfigShow(app.ConfigShowOptions{
				Dir:    cwd,
				Preset: subPreset,
				Out:    deps.stdout,
			}); err != nil {
				fmt.Fprintf(deps.stderr, "Error: %v\n", err)
				return 1
			}
			return 0

		case "init":
			sub := flag.NewFlagSet("config init", flag.ContinueOnError)
			sub.SetOutput(deps.stderr)
			subPreset := preset
			var globalFlag, forceFlag bool
			sub.StringVar(&subPreset, "preset", subPreset, "Preset inicial a escribir")
			sub.BoolVar(&globalFlag, "global", false, "Escribe en la configuración global de usuario")
			sub.BoolVar(&forceFlag, "force", false, "Sobrescribe si el archivo ya existe")
			sub.Usage = func() {
				fmt.Fprintln(deps.stderr, "Uso: weaver config init [--preset <nombre>] [--global] [--force]")
			}
			if err := sub.Parse(rest[2:]); err != nil {
				if err == flag.ErrHelp {
					return 0
				}
				return 2
			}
			if sub.NArg() > 0 {
				sub.Usage()
				return 2
			}
			if err := app.ConfigInit(app.ConfigInitOptions{
				Dir:    cwd,
				Preset: subPreset,
				Global: globalFlag,
				Force:  forceFlag,
				Out:    deps.stdout,
			}); err != nil {
				fmt.Fprintf(deps.stderr, "Error: %v\n", err)
				return 1
			}
			return 0

		default:
			fmt.Fprintf(deps.stderr, "Subcomando desconocido de config: %q\nUso: weaver config <show|init> [opciones]\n", rest[1])
			return 2
		}

	case "doctor":
		if len(rest) > 1 {
			fmt.Fprintf(deps.stderr, "Uso: weaver doctor\n")
			return 2
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return app.Doctor(ctx, cwd, deps.stdout)

	default:
		fmt.Fprintf(deps.stderr, "Subcomando desconocido: %q\n\n", rest[0])
		usage(deps.stderr)
		return 2
	}
}
