package interp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Local is a php binary on the host.
type Local struct {
	// Path is the resolved (symlink free) path of the binary.
	Path string
	// Found is the path where the binary was found, before resolving
	// symlinks (e.g. /opt/homebrew/bin/php).
	Found string
	// PHPVersion is the version detected during discovery; empty if unknown.
	PHPVersion string
}

var _ Interpreter = (*Local)(nil)

// NewLocal returns a local interpreter for the binary at path. Symlinks are
// resolved when possible.
func NewLocal(path string) *Local {
	real := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		real = r
	}
	return &Local{Path: real, Found: path}
}

// ID implements Interpreter.
func (l *Local) ID() string { return "local:" + l.Path }

// Label implements Interpreter.
func (l *Local) Label() string {
	shown := l.Found
	if shown == "" {
		shown = l.Path
	}
	if l.PHPVersion != "" {
		return "PHP " + l.PHPVersion + " (" + shown + ")"
	}
	return "PHP (" + shown + ")"
}

// Kind implements Interpreter.
func (l *Local) Kind() Kind { return KindLocal }

// Version implements Interpreter. A successful query is cached in PHPVersion.
func (l *Local) Version(ctx context.Context) (string, error) {
	v, err := QueryVersion(ctx, l)
	if err != nil {
		return "", err
	}
	l.PHPVersion = v
	return v, nil
}

// Modules implements Interpreter.
func (l *Local) Modules(ctx context.Context) ([]string, error) { return QueryModules(ctx, l) }

// Ini implements Interpreter.
func (l *Local) Ini(ctx context.Context) (IniValues, error) { return QueryIni(ctx, l) }

// Command implements Interpreter.
func (l *Local) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, l.Path, args...), nil
}

// DiscoverOptions controls Discover.
type DiscoverOptions struct {
	// Dirs, when non-nil, replaces the default search directories entirely.
	// Tests use it to stay independent of the machine.
	Dirs []string
	// PathEnv is the PATH used for the default directories; empty means
	// the PATH of the current process.
	PathEnv string
	// Home is the home directory for the default directories; empty means
	// os.UserHomeDir.
	Home string
	// Timeout bounds each version query; zero means 5 seconds.
	Timeout time.Duration
}

// DefaultSearchDirs returns the directories searched for php binaries, in
// order of preference: PATH, Homebrew, system, Laravel Herd, phpenv, asdf.
// Glob patterns are expanded; directories that do not exist are dropped.
func DefaultSearchDirs(pathEnv, home string) []string {
	var patterns []string
	patterns = append(patterns, filepath.SplitList(pathEnv)...)
	patterns = append(patterns,
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/opt/homebrew/opt/php/bin",
		"/opt/homebrew/opt/php@*/bin",
		"/usr/local/opt/php/bin",
		"/usr/local/opt/php@*/bin",
		"/usr/bin",
	)
	if home != "" {
		patterns = append(patterns,
			filepath.Join(home, ".config", "herd*", "bin"),
			filepath.Join(home, "Library", "Application Support", "Herd", "bin"),
			filepath.Join(home, ".phpenv", "shims"),
			filepath.Join(home, ".phpenv", "versions", "*", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".asdf", "installs", "php", "*", "bin"),
		)
	}
	if d := os.Getenv("ASDF_DATA_DIR"); d != "" {
		patterns = append(patterns, filepath.Join(d, "shims"), filepath.Join(d, "installs", "php", "*", "bin"))
	}

	seen := map[string]bool{}
	var dirs []string
	for _, p := range patterns {
		if p == "" {
			continue
		}
		matches := []string{p}
		if strings.ContainsAny(p, "*?[") {
			m, err := filepath.Glob(p)
			if err != nil {
				continue
			}
			sort.Strings(m)
			matches = m
		}
		for _, d := range matches {
			d = filepath.Clean(d)
			if seen[d] {
				continue
			}
			if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
				continue
			}
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// binaryPatterns are the file names considered php binaries in a directory.
func binaryPatterns() []string {
	if runtime.GOOS == "windows" {
		return []string{"php.exe"}
	}
	// php8.3 / php83 as installed by Debian/Ubuntu and some version managers.
	return []string{"php", "php[0-9]*"}
}

// FindBinaries lists executable php binaries in dirs, deduplicated by their
// real path. The first occurrence wins, so dirs order is preference order.
func FindBinaries(dirs []string) []*Local {
	seen := map[string]bool{}
	var found []*Local
	for _, dir := range dirs {
		var names []string
		for _, pat := range binaryPatterns() {
			m, err := filepath.Glob(filepath.Join(dir, pat))
			if err != nil {
				continue
			}
			sort.Strings(m)
			names = append(names, m...)
		}
		for _, p := range names {
			if !isPHPName(filepath.Base(p)) || !isExecutable(p) {
				continue
			}
			l := NewLocal(p)
			if seen[l.Path] {
				continue
			}
			seen[l.Path] = true
			found = append(found, l)
		}
	}
	return found
}

// isPHPName accepts "php", "php.exe" and versioned names like "php8.3" or
// "php83", but not "php-config", "phpize" or "php8.3-fpm".
func isPHPName(name string) bool {
	name = strings.TrimSuffix(name, ".exe")
	if name == "php" {
		return true
	}
	rest := strings.TrimPrefix(name, "php")
	if rest == name || rest == "" {
		return false
	}
	for _, r := range rest {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Discover finds local php binaries and queries their versions. Binaries that
// fail to report a version (broken shims, wrong architecture) are skipped.
func Discover(ctx context.Context, opts DiscoverOptions) []*Local {
	dirs := opts.Dirs
	if dirs == nil {
		pathEnv := opts.PathEnv
		if pathEnv == "" {
			pathEnv = os.Getenv("PATH")
		}
		home := opts.Home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		dirs = DefaultSearchDirs(pathEnv, home)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var result []*Local
	for _, l := range FindBinaries(dirs) {
		if ctx.Err() != nil {
			break
		}
		qctx, cancel := context.WithTimeout(ctx, timeout)
		_, err := l.Version(qctx)
		cancel()
		if err != nil {
			continue
		}
		result = append(result, l)
	}
	return result
}
