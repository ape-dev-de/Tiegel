// Command tiegel is the Tiegel PHP IDE.
//
//	tiegel [DIR]                         start the GUI (optionally with a project)
//	tiegel --headless <command> [...]    run without a window (see docs/headless.md)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/headless"
	"github.com/ape-dev-de/tiegel/internal/modules"
	"github.com/ape-dev-de/tiegel/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string) int {
	opts := api.Options{}
	for _, a := range args {
		if a == "--headless" {
			return headless.Run(ctx, args, headless.Env{
				Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Options: opts,
			})
		}
	}
	project, showVersion, err := guiArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: %v\nusage: tiegel [--project DIR | DIR]   or   tiegel --headless help\n", err)
		return headless.ExitUsage
	}
	if showVersion {
		fmt.Println(version.String())
		return headless.ExitOK
	}
	if dir := os.Getenv("TIEGEL_CONFIG_DIR"); dir != "" {
		opts.ConfigDir = dir
	}
	opts.Extensions = append(opts.Extensions, modules.Install)
	app, err := api.New(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: %v\n", err)
		return headless.ExitError
	}
	defer app.Close()
	if project != "" {
		if _, err := app.OpenProject(ctx, project); err != nil {
			fmt.Fprintf(os.Stderr, "tiegel: %v\n", err)
			return headless.ExitError
		}
	}
	if err := runGUI(ctx, app, project); err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: %v\n", err)
		return headless.ExitError
	}
	return headless.ExitOK
}

// guiArgs parses the GUI command line: an optional project directory given
// as --project DIR or as the only positional argument.
func guiArgs(args []string) (project string, showVersion bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--project":
			if i+1 >= len(args) {
				return "", false, fmt.Errorf("--project needs a directory")
			}
			i++
			project = args[i]
		case strings.HasPrefix(a, "--project="):
			project = strings.TrimPrefix(a, "--project=")
		case a == "--version":
			showVersion = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag %s", a)
		case project == "":
			project = a
		default:
			return "", false, fmt.Errorf("unexpected argument %s", a)
		}
	}
	return project, showVersion, nil
}
