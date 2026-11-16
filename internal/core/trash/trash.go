// Package trash moves files and directories to the desktop trash instead of
// deleting them, without external programs or dependencies:
//
//   - macOS: NSFileManager trashItemAtURL (Foundation via cgo; the same
//     call Finder uses, including "Put Back"). Without cgo the trash is
//     unavailable.
//   - Linux and other Unix systems: the freedesktop.org trash
//     specification in the home trash ($XDG_DATA_HOME/Trash). Files on
//     another file system than the home trash are not moved (ErrUnavailable).
//   - Windows: unavailable for now.
//
// Callers fall back to permanent deletion only on explicit request.
package trash

import "errors"

// ErrUnavailable is returned when the platform has no usable trash for a
// path. Nothing was changed then.
var ErrUnavailable = errors.New("trash: not available")

// Move moves path (a file, symlink or directory) to the trash.
func Move(path string) error { return moveToTrash(path) }
