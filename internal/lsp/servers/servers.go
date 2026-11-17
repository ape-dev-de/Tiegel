// Package servers locates PHP language server executables that are already
// installed on the system. It never downloads anything (decisions D3,
// D194: installation only through a package manager on an explicit click,
// see package install).
//
// The servers themselves are declared by plugins (lsp-phpactor,
// lsp-phpantom, lsp-intelephense; decision D192): each plugin contributes
// a Definition, this package only knows how to find one.
package servers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
)

// Definition describes a language server.
type Definition struct {
	// ID is a stable identifier, e.g. "phpactor".
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// Binaries are executable base names to look for, in order.
	Binaries []string `json:"binaries"`
	// Args are passed to the binary to start the server on stdio.
	Args []string `json:"args"`
	// License is the SPDX identifier or a short description.
	License string `json:"license"`
	// Proprietary marks servers that are not open source.
	Proprietary bool `json:"proprietary,omitempty"`
	// PHPVersionOption is the initializationOptions key that sets the PHP
	// version, if the server supports one.
	PHPVersionOption string `json:"phpVersionOption,omitempty"`
	// PHPVersionSetting is the dotted workspace/configuration path that sets
	// the PHP version, if the server reads it from there.
	PHPVersionSetting string `json:"phpVersionSetting,omitempty"`
}

// Server is a detected installation.
type Server struct {
	Definition
	// Path is the absolute path of the executable.
	Path string
}

// Command returns the executable and its arguments.
func (s Server) Command() (string, []string) {
	return s.Path, append([]string(nil), s.Args...)
}

// Options returns initializationOptions and workspace settings for the
// given PHP version (e.g. "8.3"). Both maps are non-nil and may be merged
// with user configuration before being passed to the LSP client.
func (d Definition) Options(phpVersion string) (initOptions, settings map[string]any) {
	initOptions = map[string]any{}
	settings = map[string]any{}
	if phpVersion == "" {
		return initOptions, settings
	}
	if d.PHPVersionOption != "" {
		initOptions[d.PHPVersionOption] = phpVersion
	}
	if d.PHPVersionSetting != "" {
		setNested(settings, strings.Split(d.PHPVersionSetting, "."), phpVersion)
	}
	return initOptions, settings
}

func setNested(m map[string]any, path []string, v any) {
	for _, p := range path[:len(path)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// DetectOptions control where Locate and Detect look.
type DetectOptions struct {
	// ProjectDir is the project root; its vendor/bin is searched first.
	ProjectDir string
	// HomeDir defaults to os.UserHomeDir().
	HomeDir string
	// PathEnv defaults to $PATH.
	PathEnv string
	// ComposerHome defaults to $COMPOSER_HOME.
	ComposerHome string
	// ExtraDirs are searched after everything else.
	ExtraDirs []string
	// NoSystemDirs skips the built-in well-known directories (Homebrew,
	// /usr/local/bin, ...) and the login shell PATH; mainly for tests.
	NoSystemDirs bool
	// LoginPath returns the PATH of the user's login shell (macOS GUI
	// apps start with a minimal PATH). Nil uses shellenv.Default; it is
	// only consulted without NoSystemDirs.
	LoginPath func() string
}

func (opts DetectOptions) loginPath() string {
	if opts.NoSystemDirs {
		return ""
	}
	if opts.LoginPath != nil {
		return opts.LoginPath()
	}
	p, _ := shellenv.Default().LoginPath(context.Background())
	return p
}

// SearchDirs returns the directories Locate searches, in order.
func SearchDirs(opts DetectOptions) []string {
	home := opts.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	pathEnv := opts.PathEnv
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	composerHome := opts.ComposerHome
	if composerHome == "" {
		composerHome = os.Getenv("COMPOSER_HOME")
	}

	var dirs []string
	if opts.ProjectDir != "" {
		dirs = append(dirs, filepath.Join(opts.ProjectDir, "vendor", "bin"))
	}
	dirs = append(dirs, filepath.SplitList(pathEnv)...)
	dirs = append(dirs, filepath.SplitList(opts.loginPath())...)
	dirs = append(dirs, ComposerBinDirs(home, composerHome)...)
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "bin"),
		)
		if runtime.GOOS == "windows" {
			if appData := os.Getenv("APPDATA"); appData != "" {
				dirs = append(dirs, filepath.Join(appData, "npm"))
			}
		}
	}
	// GUI apps on macOS start with a minimal PATH, so look at the usual
	// package manager locations explicitly.
	if !opts.NoSystemDirs && runtime.GOOS != "windows" {
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	}
	dirs = append(dirs, opts.ExtraDirs...)
	return dedupe(dirs)
}

// ComposerBinDirs returns the usual bin directories of "composer global"
// installations (COMPOSER_HOME first).
func ComposerBinDirs(home, composerHome string) []string {
	var dirs []string
	if composerHome != "" {
		dirs = append(dirs, filepath.Join(composerHome, "vendor", "bin"))
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".composer", "vendor", "bin"),
			filepath.Join(home, ".config", "composer", "vendor", "bin"),
		)
		if runtime.GOOS == "windows" {
			if appData := os.Getenv("APPDATA"); appData != "" {
				dirs = append(dirs, filepath.Join(appData, "Composer", "vendor", "bin"))
			}
		}
	}
	return dirs
}

func dedupe(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// Locate returns the first installation of def in dirs (see SearchDirs).
func Locate(def Definition, dirs []string) (Server, bool) {
	for _, dir := range dirs {
		for _, bin := range def.Binaries {
			if p, ok := findExecutable(dir, bin); ok {
				return Server{Definition: def, Path: p}, true
			}
		}
	}
	return Server{}, false
}

// Detect returns all installations of defs, in the order of defs and then
// of the search directories. A server found in several places is listed
// once per distinct executable.
func Detect(defs []Definition, opts DetectOptions) []Server {
	dirs := SearchDirs(opts)
	var out []Server
	for _, def := range defs {
		seen := map[string]bool{}
		for _, dir := range dirs {
			for _, bin := range def.Binaries {
				p, ok := findExecutable(dir, bin)
				if !ok {
					continue
				}
				key := p
				if real, err := filepath.EvalSymlinks(p); err == nil {
					key = real
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Server{Definition: def, Path: p})
			}
		}
	}
	return out
}

// Errors of CheckExecutable.
var (
	ErrNotFound      = errors.New("not found")
	ErrNotExecutable = errors.New("not executable")
)

// CheckExecutable reports whether path is an existing executable file. A
// bare name (no separator) is looked up in dirs.
func CheckExecutable(path string, dirs []string) (string, error) {
	if !strings.ContainsAny(path, `/\`) {
		for _, d := range dirs {
			if p, ok := findExecutable(d, path); ok {
				return p, nil
			}
		}
		return "", fmt.Errorf("%s: %w in %d searched directories", path, ErrNotFound, len(dirs))
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("%s: %w", path, ErrNotFound)
	case fi.IsDir():
		return "", fmt.Errorf("%s is a directory: %w", path, ErrNotExecutable)
	case !isExecutable(path):
		return "", fmt.Errorf("%s: %w (missing execute permission)", path, ErrNotExecutable)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

func findExecutable(dir, name string) (string, bool) {
	candidates := []string{name}
	if runtime.GOOS == "windows" {
		candidates = []string{name + ".exe", name + ".bat", name + ".cmd", name}
	}
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if isExecutable(p) {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			return p, true
		}
	}
	return "", false
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}
