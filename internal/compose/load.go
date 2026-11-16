// Package compose reads Docker Compose projects, detects PHP services, derives
// host/container path mappings and runs PHP inside a service. It wraps
// github.com/compose-spec/compose-go/v2 for parsing, interpolation and
// profiles, and shells out to `docker compose` for execution.
package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ape-dev-de/tiegel/internal/core/fspath"
)

// ErrNoComposeFile is returned when a directory contains no compose file.
var ErrNoComposeFile = errors.New("compose: no compose file found")

// DefaultFileNames are the compose file names looked up in a project
// directory, in order of preference. Only the first match is used.
var DefaultFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// OverrideFileNames are the override file names; the first match is loaded on
// top of the main file.
var OverrideFileNames = []string{"compose.override.yaml", "compose.override.yml", "docker-compose.override.yml", "docker-compose.override.yaml"}

// Files describes which compose files make up a project.
type Files struct {
	// Dir is the absolute project directory.
	Dir string
	// Paths are the absolute compose file paths in load order.
	Paths []string
	// FromEnv is true when Paths came from COMPOSE_FILE.
	FromEnv bool
}

// FindFiles determines the compose files for dir. COMPOSE_FILE is honoured
// from env first, then from dir/.env (separator COMPOSE_PATH_SEPARATOR,
// default os.PathListSeparator). Otherwise the first of DefaultFileNames and
// the first of OverrideFileNames are used. Parent directories are not
// searched.
func FindFiles(dir string, env map[string]string) (Files, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Files{}, fmt.Errorf("compose: resolve %s: %w", dir, err)
	}
	f := Files{Dir: abs}

	vars, err := readDotEnv(abs, env)
	if err != nil {
		return f, err
	}
	for k, v := range env {
		vars[k] = v
	}
	if list, ok := vars["COMPOSE_FILE"]; ok && strings.TrimSpace(list) != "" {
		sep := vars["COMPOSE_PATH_SEPARATOR"]
		if sep == "" {
			sep = string(os.PathListSeparator)
		}
		for _, p := range strings.Split(list, sep) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !fspath.IsAbs(p) {
				p = filepath.Join(abs, p)
			}
			if !isFile(p) {
				return f, fmt.Errorf("compose: COMPOSE_FILE entry %s: %w", p, os.ErrNotExist)
			}
			f.Paths = append(f.Paths, p)
		}
		f.FromEnv = true
		return f, nil
	}

	if main := firstExisting(abs, DefaultFileNames); main != "" {
		f.Paths = append(f.Paths, main)
		if ov := firstExisting(abs, OverrideFileNames); ov != "" {
			f.Paths = append(f.Paths, ov)
		}
		return f, nil
	}
	return f, fmt.Errorf("%w in %s", ErrNoComposeFile, abs)
}

func readDotEnv(dir string, env map[string]string) (map[string]string, error) {
	p := filepath.Join(dir, ".env")
	if !isFile(p) {
		return map[string]string{}, nil
	}
	vars, err := dotenv.GetEnvFromFile(env, []string{p})
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", p, err)
	}
	return vars, nil
}

func firstExisting(dir string, names []string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if isFile(p) {
			return p
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// LoadOptions controls Load.
type LoadOptions struct {
	// Files overrides compose file discovery when non-empty.
	Files []string
	// Env holds variables that take precedence over .env, like the shell
	// environment does for `docker compose`. The process environment is not
	// read implicitly, which keeps loading deterministic.
	Env map[string]string
	// Profiles to activate. Services in other profiles end up in
	// Project.Model.DisabledServices. "*" activates all profiles.
	Profiles []string
	// Name overrides the project name.
	Name string
}

// Project is a loaded compose project.
type Project struct {
	// Name is the compose project name (passed as -p to docker compose).
	Name string
	// Dir is the absolute project directory.
	Dir string
	// Files are the compose files in load order.
	Files []string
	// Profiles are the active profiles.
	Profiles []string
	// Model is the fully interpolated and merged compose model.
	Model *types.Project
}

// Load finds and parses the compose files of dir. Variables are interpolated
// from opts.Env and dir/.env; relative paths are resolved against dir.
func Load(ctx context.Context, dir string, opts LoadOptions) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("compose: resolve %s: %w", dir, err)
	}
	files := opts.Files
	if len(files) == 0 {
		found, err := FindFiles(abs, opts.Env)
		if err != nil {
			return nil, err
		}
		files = found.Paths
	}

	env := make([]string, 0, len(opts.Env)+1)
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	if _, ok := opts.Env["PWD"]; !ok {
		// docker compose sees the shell's PWD; Sail references it.
		env = append(env, "PWD="+abs)
	}
	sort.Strings(env)

	fns := []cli.ProjectOptionsFn{
		cli.WithWorkingDirectory(abs),
		cli.WithEnv(env),
		cli.WithEnvFiles(),
		cli.WithDotEnv,
		cli.WithResolvedPaths(true),
		cli.WithProfiles(opts.Profiles),
	}
	if opts.Name != "" {
		fns = append(fns, cli.WithName(opts.Name))
	}
	po, err := cli.NewProjectOptions(files, fns...)
	if err != nil {
		return nil, fmt.Errorf("compose: options: %w", err)
	}
	model, err := po.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose: load %s: %w", abs, err)
	}
	return &Project{
		Name:     model.Name,
		Dir:      abs,
		Files:    files,
		Profiles: append([]string(nil), opts.Profiles...),
		Model:    model,
	}, nil
}

// Service returns the enabled service called name.
func (p *Project) Service(name string) (types.ServiceConfig, bool) {
	if p == nil || p.Model == nil {
		return types.ServiceConfig{}, false
	}
	s, ok := p.Model.Services[name]
	return s, ok
}

// ServiceNames returns the enabled services, sorted.
func (p *Project) ServiceNames() []string {
	if p == nil || p.Model == nil {
		return nil
	}
	names := make([]string, 0, len(p.Model.Services))
	for n := range p.Model.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
