package trash

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// homeTrash returns the home trash directory of the freedesktop.org trash
// specification: $XDG_DATA_HOME/Trash, by default ~/.local/share/Trash.
func homeTrash(dataHome, home string) (string, error) {
	if dataHome == "" {
		if home == "" {
			return "", fmt.Errorf("%w: no home directory", ErrUnavailable)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "Trash"), nil
}

// moveXDG moves path into the trash directory dir (files/ + info/*.trashinfo).
// The move is a rename: across file systems it fails with ErrUnavailable
// and nothing changes.
func moveXDG(dir, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	if _, err := os.Lstat(abs); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	files, info := filepath.Join(dir, "files"), filepath.Join(dir, "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("trash: %w", err)
		}
	}
	base := filepath.Base(abs)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = stem + "." + strconv.Itoa(i) + ext
		}
		// The info file reserves the name (O_EXCL), as the spec requires.
		infoPath := filepath.Join(info, name+".trashinfo")
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("trash: %w", err)
		}
		u := (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath()
		_, werr := fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", u, time.Now().Format("2006-01-02T15:04:05"))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(infoPath)
			return fmt.Errorf("trash: %w", werr)
		}
		dst := filepath.Join(files, name)
		if _, err := os.Lstat(dst); err == nil {
			_ = os.Remove(infoPath)
			continue
		}
		if err := os.Rename(abs, dst); err != nil {
			_ = os.Remove(infoPath)
			if errors.Is(err, syscall.EXDEV) {
				return fmt.Errorf("%w: %s is on another file system than %s", ErrUnavailable, abs, dir)
			}
			return fmt.Errorf("trash: %w", err)
		}
		return nil
	}
	return fmt.Errorf("trash: no free name for %s", base)
}
