// Package servers detects PHP language servers that are already installed
// on the system. It never downloads anything (see docs/DECISIONS.md, D3).
package servers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Server IDs.
const (
	Phpactor     = "phpactor"
	PHPantom     = "phpantom"
	Intelephense = "intelephense"
)

// Definition describes a known language server.
type Definition struct {
	// ID is a stable identifier, e.g. "phpactor".
	ID string
	// Name is the display name.
	Name string
	// Binaries are executable base names to look for, in order.
	Binaries []string
	// Args are passed to the binary to start the server on stdio.
	Args []string
	// License is the SPDX identifier or a short description.
	License string
	// Proprietary marks servers that are not open source.
	Proprietary bool
	// PHPVersionOption is the initializationOptions key that sets the PHP
	// version, if the server supports one.
	PHPVersionOption string
	// PHPVersionSetting is the dotted workspace/configuration path that sets
	// the PHP version, if the server reads it from there.
	PHPVersionSetting string
}

// Known lists the supported servers in order of preference: Phpactor is
// the default, PHPantom the open-source alternative, Intelephense optional.
func Known() []Definition {
	return []Definition{
		{
			ID: Phpactor, Name: "Phpactor", Binaries: []string{"phpactor"},
			Args: []string{"language-server"}, License: "MIT",
			PHPVersionOption: "php.version",
		},
		{
			ID: PHPantom, Name: "PHPantom", Binaries: []string{"phpantom_lsp", "phpantom"},
			License: "MIT",
		},
		{
			ID: Intelephense, Name: "Intelephense", Binaries: []string{"intelephense"},
			Args: []string{"--stdio"}, License: "proprietary", Proprietary: true,
			PHPVersionSetting: "intelephense.environment.phpVersion",
		},
	}
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

// DetectOptions control where Detect looks.
type DetectOptions struct {
	// ProjectDir is the project root; its vendor/bin is searched first.
	ProjectDir string
	// HomeDir defaults to os.UserHomeDir().
	HomeDir string
	// PathEnv defaults to $PATH.
	PathEnv string
	// ComposerHome defaults to $COMPOSER_HOME.
	ComposerHome string
	// IncludeProprietary also returns proprietary servers.
	IncludeProprietary bool
	// ExtraDirs are searched after everything else.
	ExtraDirs []string
	// NoSystemDirs skips the built-in well-known directories (Homebrew,
	// /usr/local/bin, ...); mainly for tests.
	NoSystemDirs bool
}

// SearchDirs returns the directories Detect searches, in order.
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
	if composerHome != "" {
		dirs = append(dirs, filepath.Join(composerHome, "vendor", "bin"))
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".composer", "vendor", "bin"),
			filepath.Join(home, ".config", "composer", "vendor", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "bin"),
		)
		if runtime.GOOS == "windows" {
			if appData := os.Getenv("APPDATA"); appData != "" {
				dirs = append(dirs, filepath.Join(appData, "Composer", "vendor", "bin"), filepath.Join(appData, "npm"))
			}
		}
	}
	// GUI apps on macOS start with a minimal PATH, so look at the usual
	// package manager locations explicitly.
	if !opts.NoSystemDirs && runtime.GOOS != "windows" {
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	}
	dirs = append(dirs, opts.ExtraDirs...)

	seen := make(map[string]bool, len(dirs))
	out := dirs[:0]
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

// Detect returns all installed servers, ordered by Known() preference and
// then by search order. A server found in several places is listed once
// per distinct executable.
func Detect(opts DetectOptions) []Server {
	dirs := SearchDirs(opts)
	var out []Server
	for _, def := range Known() {
		if def.Proprietary && !opts.IncludeProprietary {
			continue
		}
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

// Find returns the preferred installation of the server with the given ID.
func Find(id string, opts DetectOptions) (Server, bool) {
	if def, ok := lookup(id); ok && def.Proprietary {
		opts.IncludeProprietary = true
	}
	for _, s := range Detect(opts) {
		if s.ID == id {
			return s, true
		}
	}
	return Server{}, false
}

func lookup(id string) (Definition, bool) {
	for _, d := range Known() {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
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
