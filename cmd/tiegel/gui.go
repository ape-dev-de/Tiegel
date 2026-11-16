package main

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui"
)

// runGUI starts the graphical shell (internal/app/gui). The GUI is only a
// client of app (D41); Gio is initialised nowhere else, so the headless
// path never touches a display. gui.Run owns the main goroutine (Gio's
// app.Main) and exits the process when the window closes, after saving
// modified documents and closing app.
func runGUI(ctx context.Context, app *api.App, projectPath string) error {
	return gui.Run(ctx, app, projectPath)
}
