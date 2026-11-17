//go:build !darwin || !cgo || ios

package native

import "context"

// FolderDialogSupported reports whether ChooseFolder shows a native dialog.
// It is always false on this platform.
func FolderDialogSupported() bool { return false }

// ChooseFolder would show the native folder chooser; on this platform it
// returns ErrUnsupported (or ctx.Err() if ctx is already done).
func ChooseFolder(ctx context.Context, title, dir string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return "", false, ErrUnsupported
}

// MenuSupported reports whether SetMenus installs a native menu bar. It is
// always false on this platform.
func MenuSupported() bool { return false }

// SetMenus does nothing on this platform.
func SetMenus(menus []Menu, onAction func(id string)) {}

// GesturesSupported reports whether SetGestureHandler delivers mouse side
// buttons and swipes. It is always false on this platform (Gio reports
// the side buttons itself on Windows and Wayland).
func GesturesSupported() bool { return false }

// SetGestureHandler does nothing on this platform.
func SetGestureHandler(fn func(gesture string)) {}
