// Package shellenv resolves the PATH a user's login shell would see.
//
// GUI apps on macOS are started by launchd with a minimal PATH
// (/usr/bin:/bin:/usr/sbin:/sbin), so programs installed via Homebrew,
// Composer global or OrbStack are not found with exec.LookPath. LoginPath
// asks the login shell once and caches the result; callers merge it with
// os.Getenv("PATH") via Path.
package shellenv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Runner runs name with args and returns stdout. Replaceable in tests.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Resolver caches the login shell PATH. The zero value is not usable; use
// New or Default.
type Resolver struct {
	run     Runner
	shell   string
	timeout time.Duration

	once sync.Once
	path string
	err  error
}

// New returns a resolver using shell (empty: $SHELL, then /bin/zsh) and run
// (nil: os/exec).
func New(shell string, run Runner) *Resolver {
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/zsh"
	}
	if run == nil {
		run = execRunner
	}
	return &Resolver{run: run, shell: shell, timeout: 3 * time.Second}
}

var (
	defaultOnce sync.Once
	defaultRes  *Resolver
)

// Default returns the process-wide resolver.
func Default() *Resolver {
	defaultOnce.Do(func() { defaultRes = New("", nil) })
	return defaultRes
}

const marker = "__TIEGEL_PATH__"

// LoginPath returns the PATH of an interactive login shell, asked once.
// On Windows it returns "" and no error. A shell failure or timeout is
// returned as error (and cached); callers fall back to os.Getenv("PATH").
func (r *Resolver) LoginPath(ctx context.Context) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	r.once.Do(func() {
		ctx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		// Markers guard against noise printed by shell startup files.
		out, err := r.run(ctx, r.shell, "-l", "-i", "-c", "printf '"+marker+"%s"+marker+"' \"$PATH\"")
		if err != nil {
			r.err = fmt.Errorf("shellenv: login shell %s: %w", r.shell, err)
			return
		}
		s := string(out)
		i := strings.Index(s, marker)
		j := strings.LastIndex(s, marker)
		if i < 0 || j <= i {
			r.err = fmt.Errorf("shellenv: no PATH in output of %s", r.shell)
			return
		}
		r.path = s[i+len(marker) : j]
	})
	return r.path, r.err
}

// Path returns the process PATH followed by login-shell entries that are
// missing from it, without duplicates. Errors from the login shell are
// ignored (the process PATH is returned).
func (r *Resolver) Path(ctx context.Context) string {
	login, _ := r.LoginPath(ctx)
	return Merge(os.Getenv("PATH"), login)
}

// Merge joins PATH lists, keeping the first occurrence of each entry.
func Merge(lists ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, d := range filepath.SplitList(l) {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// LookPath searches name in dirs (a PATH list) and returns the first
// executable match.
func LookPath(name, dirs string) (string, error) {
	for _, d := range filepath.SplitList(dirs) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		if runtime.GOOS == "windows" && filepath.Ext(p) == "" {
			for _, ext := range []string{".exe", ".bat", ".cmd"} {
				if isExec(p + ext) {
					return p + ext, nil
				}
			}
			continue
		}
		if isExec(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("shellenv: %s: %w", name, exec.ErrNotFound)
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}
