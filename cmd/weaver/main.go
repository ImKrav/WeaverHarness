package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/ImKrav/weaverharness/internal/app"
)

func usage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, `Uso: weaver [comando]

Comandos:
  (sin argumentos)     Inicia una sesión interactiva (TUI) en el repositorio actual
  resume [run-id]      Reanuda la última ejecución o la indicada por run-id
  runs                 Lista las ejecuciones guardadas en .weaver/runs
  doctor               Comprueba instalación, sesión y modelos de los CLIs proveedores

Opciones:
  -h, --help           Muestra esta ayuda`)
}

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	fs := flag.NewFlagSet("weaver", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(fs) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	rest := fs.Args()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error obteniendo directorio actual: %v\n", err)
		return 1
	}

	if len(rest) == 0 {
		if err := app.RunSession(context.Background(), app.SessionOptions{Dir: cwd}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	switch rest[0] {
	case "resume":
		var runID string
		if len(rest) > 1 {
			runID = rest[1]
		}
		if err := app.RunSession(context.Background(), app.SessionOptions{
			Dir:      cwd,
			Resume:   true,
			ResumeID: runID,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error al reanudar: %v\n", err)
			return 1
		}
		return 0

	case "runs":
		if err := app.Runs(cwd, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case "doctor":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return app.Doctor(ctx, cwd, os.Stdout)

	default:
		fmt.Fprintf(os.Stderr, "Subcomando desconocido: %q\n\n", rest[0])
		usage(fs)
		return 2
	}
}
