package qakit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// copyDirPrefix starts the name of the directory holding a formatter's
// copy inside the project (container runs). A leading dot and ".tiegel-"
// make the project watcher ignore it (watch.Watcher.excluded).
const copyDirPrefix = ".tiegel-fmt-"

// staleCopyAge: copy directories in the project older than this are
// leftovers of a crashed run (longer than the maximum timeout).
const staleCopyAge = 2 * time.Hour

// formatCopy is the copy of a file a formatter fixes in place (D360). It
// keeps the file's base name and its path relative to the project, so
// rules that derive class or namespace names from the path see the real
// names:
//
//   - runs on the host (no interpreter, local php) use a private
//     directory under the system temp directory, invisible to anything
//     watching the project;
//   - runs in a container or WSL need the copy inside the mapped project:
//     <root>/.tiegel-fmt-<random>/<relative path>, ignored by Tiegel's
//     watcher and removed afterwards.
type formatCopy struct {
	// dir is the directory removed afterwards; path the copy on the host.
	dir, path string
	// arg names the copy for the tool: absolute on the host, relative to
	// the project root (valid inside the container) otherwise.
	arg string
	// token is the random part of dir; a result containing it mentions
	// the temporary location and is discarded.
	token string
}

// newFormatCopy writes content as the copy of file (below root). private
// selects the system temp directory (host runs). Nothing is left behind
// on errors.
func newFormatCopy(root, file string, content []byte, private bool) (*formatCopy, error) {
	rel, err := filepath.Rel(root, file)
	if err != nil || !within(root, file) || rel == "." {
		return nil, fmt.Errorf("%s is outside the project", file)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("temporary copy: %w", err)
	}
	c := &formatCopy{token: hex.EncodeToString(b[:])}
	if private {
		dir, err := os.MkdirTemp("", "tiegel-fmt-"+c.token+"-")
		if err != nil {
			return nil, fmt.Errorf("temporary copy: %w", err)
		}
		c.dir = dir
	} else {
		removeStaleCopies(root, time.Now())
		c.dir = filepath.Join(root, copyDirPrefix+c.token)
		// Readable for the tool inside the container (another user).
		if err := os.Mkdir(c.dir, 0o755); err != nil {
			return nil, fmt.Errorf("temporary copy: %w", err)
		}
	}
	c.path = filepath.Join(c.dir, rel)
	if private {
		c.arg = c.path
	} else {
		r, _ := filepath.Rel(root, c.path)
		c.arg = filepath.ToSlash(r)
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(file); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		c.remove()
		return nil, fmt.Errorf("temporary copy: %w", err)
	}
	if err := os.WriteFile(c.path, content, mode); err != nil {
		c.remove()
		return nil, fmt.Errorf("temporary copy: %w", err)
	}
	return c, nil
}

// remove deletes the copy and its directory.
func (c *formatCopy) remove() {
	if c != nil && c.dir != "" {
		_ = os.RemoveAll(c.dir)
	}
}

// removeStaleCopies deletes copy directories in root left behind by a
// crash (older than staleCopyAge at now).
func removeStaleCopies(root string, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), copyDirPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > staleCopyAge {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
