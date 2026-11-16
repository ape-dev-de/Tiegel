//go:build headless

package main

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// guiAvailable is false in headless-only builds (build tag "headless"). They
// contain no Gio code and therefore build without cgo on macOS and Linux
// (docs/plattformen.md, D84).
const guiAvailable = false

// runGUI reports that the GUI is not part of this build.
func runGUI(context.Context, *api.App, api.Options, string) error {
	return errNoGUI
}
