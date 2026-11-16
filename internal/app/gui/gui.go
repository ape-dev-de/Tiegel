// Package gui is Tiegel's graphical shell: the Gio window with the main
// workbench (project tree, editor tabs, editor, tool windows, status bar),
// the welcome screen, choosers and the settings dialog.
//
// The GUI is a client of the API (D41): all state and logic live in the
// Gio-free package controller, which calls API methods by name through a
// Backend and reacts to bus events; the View only lays out organisms from
// internal/ui and turns their events into controller calls. Editors edit
// the shared document buffers in-process (api.App.Document) under the
// document lock.
//
// Extension points for further features (git tool window, debug panel,
// gutter markers, actions, status items) are collected in Registry.
package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
)

// processStart approximates the process start (package initialisation).
var processStart = time.Now()

// Run opens the main window for app and runs until it is closed or ctx is
// cancelled. With projectPath the project is shown (it may already be open
// in app); without it the welcome screen appears.
//
// Gio's app.Main must own the main goroutine and never returns, so the
// window loop runs on a goroutine; when it ends (modified documents are
// saved on close), Run closes app and exits the process (exit code 1 on
// error).
func Run(ctx context.Context, a *api.App, projectPath string) error {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("Tiegel"), app.Size(unit.Dp(1280), unit.Dp(820)), app.MinSize(unit.Dp(640), unit.Dp(400)))
		err := loop(ctx, w, controller.NewInProcess(a), projectPath)
		_ = a.Close()
		os.Exit(exitCode(err))
	}()
	app.Main()
	return nil
}

func exitCode(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: %v\n", err)
		return 1
	}
	return 0
}

// loop runs the window event loop.
func loop(ctx context.Context, w *app.Window, be controller.Backend, projectPath string) error {
	ctl := controller.New(be)
	defer ctl.Close()
	ctl.Invalidate = w.Invalidate
	v := NewView(ctl, runtime.GOOS)
	v.Invalidate = w.Invalidate
	defer v.Close()
	ctl.Start(projectPath)
	go func() {
		<-ctx.Done()
		w.Perform(system.ActionClose)
	}()

	var ops op.Ops
	first := true
	focused := true
	title := ""
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			saveOnClose(ctl)
			return e.Err
		case app.ConfigEvent:
			if focused && !e.Config.Focused {
				ctl.Autosave("focusLost")
			}
			focused = e.Config.Focused
		case app.FrameEvent:
			if ctx.Err() != nil {
				saveOnClose(ctl)
				return nil
			}
			gtx := app.NewContext(&ops, e)
			v.Frame(gtx)
			e.Frame(gtx.Ops)
			if t := windowTitle(ctl); t != title {
				title = t
				w.Option(app.Title(t))
			}
			if first {
				first = false
				d := time.Since(processStart)
				ctl.Status.Notice = ""
				if os.Getenv("TIEGEL_STARTUP_TRACE") != "" {
					fmt.Fprintf(os.Stderr, "tiegel: first frame after %v\n", d.Round(time.Millisecond))
				}
			}
		}
	}
}

// saveOnClose saves modified documents synchronously before the window
// goes away (autosave on close).
func saveOnClose(ctl *controller.Controller) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ctl.AutosaveSync(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: saving on close: %v\n", err)
	}
}

func windowTitle(c *controller.Controller) string {
	if !c.Project.Open {
		return "Tiegel"
	}
	t := c.Project.Name + " – Tiegel"
	if tab := c.ActiveTab(); tab != nil {
		t = c.Project.Name + " – " + tab.Path + " – Tiegel"
	}
	return t
}

// systemDarkMode reports the OS appearance for the "system" theme. Only
// macOS is queried (local `defaults`, no network); elsewhere dark is
// assumed.
func systemDarkMode() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return err == nil && strings.Contains(string(out), "Dark")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
