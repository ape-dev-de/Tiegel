//go:build darwin && cgo && !ios

package native

/*
#cgo CFLAGS: -Werror -fobjc-arc -x objective-c
#cgo LDFLAGS: -framework AppKit

#include <stdlib.h>

int tiegel_choose_folder(const char *title, const char *dir, char **out);
void tiegel_set_menus(void *buf, size_t len);
void tiegel_install_gestures(void);
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"unsafe"
)

var (
	gestureMu sync.Mutex
	onGesture func(string)

	menuMu     sync.Mutex
	menuAction func(id string)
	menuIDs    []string
	menuGen    uint32
)

// FolderDialogSupported reports whether ChooseFolder shows a native dialog.
func FolderDialogSupported() bool { return true }

// ChooseFolder shows the native folder chooser titled title, starting in
// dir if that is non-empty and exists. It blocks until the user picks a
// folder (path, true, nil) or cancels ("", false, nil). It must not be
// called from the main thread. ctx is only checked before the dialog is
// shown.
func ChooseFolder(ctx context.Context, title, dir string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	ct, cd := C.CString(title), C.CString(dir)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cd))
	var out *C.char
	switch C.tiegel_choose_folder(ct, cd, &out) {
	case 1:
		defer C.free(unsafe.Pointer(out))
		return C.GoString(out), true, nil
	case 0:
		return "", false, nil
	default:
		return "", false, errors.New("native: ChooseFolder called on the main thread")
	}
}

// MenuSupported reports whether SetMenus installs a native menu bar.
func MenuSupported() bool { return true }

// SetMenus replaces the application's main menu with the application menu
// (About, Settings…, Hide, Hide Others, Show All, Quit) followed by menus.
// A menu titled "Window" also becomes the system windows menu. Shortcuts
// of menus are displayed only; the window performs them. SetMenus never
// blocks and may be called from any goroutine; the menu is installed
// asynchronously on the main thread.
//
// onAction receives the ID of a chosen item ("app.about", "app.settings",
// "app.quit" for the application menu). It runs on the main thread inside
// the Cocoa event loop and must return quickly, e.g. by handing the ID to
// another goroutine; it must not block on the Gio window.
func SetMenus(menus []Menu, onAction func(id string)) {
	menuMu.Lock()
	menuGen++
	buf, ids := encodeMenus(menus, menuGen)
	menuIDs, menuAction = ids, onAction
	menuMu.Unlock()
	// Ownership of the C copy passes to the Objective-C side, which frees
	// it after building the menu on the main thread.
	C.tiegel_set_menus(C.CBytes(buf), C.size_t(len(buf)))
}

//export tiegelMenuAction
func tiegelMenuAction(tag C.longlong) {
	gen, idx := uint32(uint64(tag)>>32), int(uint32(tag))
	menuMu.Lock()
	var id string
	fn := menuAction
	if gen == menuGen && idx < len(menuIDs) {
		id = menuIDs[idx]
	}
	menuMu.Unlock()
	if id != "" && fn != nil {
		fn(id)
	}
}

// GesturesSupported reports whether SetGestureHandler delivers mouse side
// buttons and swipes (Gio does not report them on macOS).
func GesturesSupported() bool { return true }

// SetGestureHandler installs (once) a Cocoa event monitor for the mouse
// side buttons (buttons 4 and 5) and trackpad swipes (with "swipe between
// pages" set to three fingers) and reports them as Gesture* names. fn
// runs on the main thread and must return quickly; nil stops reporting.
func SetGestureHandler(fn func(gesture string)) {
	gestureMu.Lock()
	onGesture = fn
	gestureMu.Unlock()
	if fn != nil {
		C.tiegel_install_gestures()
	}
}

//export tiegelGesture
func tiegelGesture(code C.int) {
	gestureMu.Lock()
	fn := onGesture
	gestureMu.Unlock()
	if g := gestureName(int(code)); g != "" && fn != nil {
		fn(g)
	}
}
