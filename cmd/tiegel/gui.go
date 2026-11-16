package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// runGUI starts the graphical shell. The Gio shell is built in
// internal/app/gui and will expose
//
//	func Run(ctx context.Context, app *api.App, projectPath string) error
//
// which this function then calls. The GUI is only a client of app (D41); Gio
// is initialised nowhere else, so the headless path never touches a display.
func runGUI(_ context.Context, _ *api.App, projectPath string) error {
	fmt.Fprintln(os.Stderr, "Tiegel: the GUI is not available yet. Use the headless mode: tiegel --headless help")
	if projectPath != "" {
		fmt.Fprintf(os.Stderr, "(project: %s)\n", projectPath)
	}
	return nil
}
