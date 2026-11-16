package native

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

// cgoNative reports whether this build uses the Cocoa implementation.
func cgoNative() bool { return runtime.GOOS == "darwin" && MenuSupported() }

func TestSupported(t *testing.T) {
	if MenuSupported() != FolderDialogSupported() {
		t.Errorf("MenuSupported = %v, FolderDialogSupported = %v", MenuSupported(), FolderDialogSupported())
	}
	if runtime.GOOS != "darwin" && MenuSupported() {
		t.Errorf("MenuSupported = true on %s", runtime.GOOS)
	}
}

func TestStubs(t *testing.T) {
	if cgoNative() {
		t.Skip("native implementation; ChooseFolder/SetMenus need a running NSApp")
	}
	SetMenus([]Menu{{Title: "File"}}, func(string) {})
	p, ok, err := ChooseFolder(context.Background(), "Open", "")
	if p != "" || ok || !errors.Is(err, ErrUnsupported) {
		t.Errorf("ChooseFolder = %q, %v, %v; want ErrUnsupported", p, ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ChooseFolder(ctx, "Open", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("ChooseFolder(cancelled) err = %v", err)
	}
}

func TestChooseFolderCancelledContext(t *testing.T) {
	// A done context returns before any dialog is shown, on all platforms.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := ChooseFolder(ctx, "Open", ""); ok || !errors.Is(err, context.Canceled) {
		t.Errorf("ChooseFolder = %v, %v; want context.Canceled", ok, err)
	}
}
