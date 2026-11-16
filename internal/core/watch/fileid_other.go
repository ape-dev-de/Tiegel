//go:build !unix

package watch

import "io/fs"

// fileID is not available from a plain FileInfo on this platform; renames
// are then paired by size and modification time.
func fileID(fs.FileInfo) uint64 { return 0 }
