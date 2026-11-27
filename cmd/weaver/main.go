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
  (sin argumentos)     Inicia una sesión interactiva (TUI) en el repositorio actual
  resume [run-id]      Reanuda la última ejecución o la indicada por run-id
  runs                 Lista las ejecuciones guardadas en .weaver/runs
  doctor               Comprueba instalación, sesión y modelos de los CLIs proveedores
  version              Muestra la versión compilada de weaver

Opciones:
  -v, --version        Muestra la versión compilada de weaver
  -h, --help           Muestra esta ayuda`)
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
	var showVersion bool
	fs.BoolVar(&showVersion, "v", false, "Muestra la versión compilada")
	fs.BoolVar(&showVersion, "version", false, "Muestra la versión compilada")
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
			Dir: cwd,
			In:  deps.stdin,
			Out: deps.stdout,
		}); err != nil {
			fmt.Fprintf(deps.stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	switch rest[0] {
	case "resume":
		if len(rest) > 2 {
			fmt.Fprintf(deps.stderr, "Uso: weaver resume [run-id]\n")
			return 2
		}
		var runID string
		if len(rest) == 2 {
			runID = rest[1]
		}
		if err := app.RunSession(context.Background(), app.SessionOptions{
			Dir:      cwd,
			Resume:   true,
			ResumeID: runID,
			In:       deps.stdin,
			Out:      deps.stdout,
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
