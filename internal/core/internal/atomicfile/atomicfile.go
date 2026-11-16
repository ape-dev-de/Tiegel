// Package atomicfile writes files atomically: content goes to a temporary
// file in the target directory, is synced, and then renamed over the target,
// so readers see either the old or the new content, never a partial write.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data. Missing parent directories are
// created with mode 0o700. If path exists, its permission bits are kept;
// otherwise perm is used.
func Write(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("atomicfile: create dir: %w", err)
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err = f.Chmod(perm); err != nil {
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("atomicfile: sync: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("atomicfile: close: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}
