package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// Compose discovery (D520): compose files are often not in the opened
// directory. A monorepo keeps them two levels above the PHP application
// ("clients/app/project" mounted by "../../docker-compose.yaml"), a project
// keeps them in "docker/". Discover looks in the project root, its
// subfolders (depth ≤ 2) and its ancestors, loads every file set the way
// `docker compose` would and ranks the services by how likely they run the
// project's PHP. Only local files are read; the Docker engine is never
// contacted.

// Relations of a discovered file set to the project root.
const (
	RelationRoot     = "root"     // compose files in the project root
	RelationSubdir   = "subdir"   // in a subfolder of the project
	RelationAncestor = "ancestor" // in a parent directory
)

// Mount relations of a discovered service to the project.
const (
	// MountCovers: a bind mount covers the PHP root (it or a parent).
	MountCovers = "covers"
	// MountInside: a bind mount covers a folder inside the project.
	MountInside = "inside"
	// MountNone: no bind mount touches the project.
	MountNone = ""
)

// Score weights of the discovery ranking (on top of ScoreService).
const (
	scoreMountsPHPRoot  = 35
	scoreMountsInside   = 20
	scoreBuiltImage     = 40
	scoreOneShot        = -30
	scoreTaskName       = -10
	scoreUnrelatedMount = -20
)

// DiscoverOptions control Discover.
type DiscoverOptions struct {
	// PHPRoot is the directory with the PHP application (composer.json);
	// empty means the project root. Services mounting exactly it rank
	// highest.
	PHPRoot string
	// Env takes precedence over .env files (see LoadOptions.Env).
	Env map[string]string
	// Home stops the upward walk (it is not searched); empty means the
	// user's home directory.
	Home string
	// MaxUp is the number of parent directories searched (default 4).
	MaxUp int
	// MaxDepth is the subfolder depth searched (default 2).
	MaxDepth int
}

// DiscoveredService is a service of a discovered file set.
type DiscoveredService struct {
	Candidate
	// Mount tells how the service mounts the project (Mount*).
	Mount string `json:"mount,omitempty"`
	// ProjectRemote is the container path of the PHP root when a bind
	// mount covers it.
	ProjectRemote string `json:"projectRemote,omitempty"`
	// Mapping is the bind mount that relates to the project (host →
	// container), if any.
	Mapping *PathMapping `json:"mapping,omitempty"`
}

// FileSet is a group of compose files loaded together.
type FileSet struct {
	// Dir is the compose project directory (of the first file).
	Dir string `json:"dir"`
	// Files are loaded by default: the main file and the standard
	// override, or COMPOSE_FILE from the environment or Dir/.env.
	Files []string `json:"files"`
	// Overlays are further compose files in Dir that `docker compose`
	// does not load by default (docker-compose.<name>.yaml,
	// *.override.<arch>.yaml); the user may add them.
	Overlays []string `json:"overlays,omitempty"`
	// Relation is RelationRoot, RelationSubdir or RelationAncestor;
	// Levels is the number of directories between Dir and the project
	// root.
	Relation string `json:"relation"`
	Levels   int    `json:"levels"`
	// Name is the compose project name.
	Name     string   `json:"name,omitempty"`
	Services []string `json:"services,omitempty"`
	// Candidates are the ranked services (best first): PHP services and
	// services mounting the project.
	Candidates []DiscoveredService `json:"candidates"`
	// Error describes a file set that could not be loaded.
	Error string `json:"error,omitempty"`

	project *Project
}

// Project returns the loaded compose project (nil when Error is set).
func (f FileSet) Project() *Project { return f.project }

// Best returns the best candidate, if any has a positive score.
func (f FileSet) Best() (DiscoveredService, bool) {
	if len(f.Candidates) == 0 || f.Candidates[0].Score <= 0 {
		return DiscoveredService{}, false
	}
	return f.Candidates[0], true
}

// Discovery is the result of Discover.
type Discovery struct {
	// Root is the project root, PHPRoot the PHP root used for ranking.
	Root    string `json:"root"`
	PHPRoot string `json:"phpRoot"`
	// Sets are the file sets found, best first (by their best candidate).
	Sets []FileSet `json:"sets"`
}

// Best returns the set and service ranked highest overall.
func (d Discovery) Best() (FileSet, DiscoveredService, bool) {
	for _, s := range d.Sets {
		if c, ok := s.Best(); ok {
			return s, c, true
		}
	}
	return FileSet{}, DiscoveredService{}, false
}

// skipDirs are never searched for compose files (compared lowercased).
var skipDirs = map[string]bool{"vendor": true, "node_modules": true, ".git": true, ".idea": true, "var": true, "cache": true, "storage": true}

// Discover finds the compose file sets around root (see DiscoverOptions)
// and ranks their services. A set that fails to load is reported with
// Error and no candidates.
func Discover(ctx context.Context, root string, o DiscoverOptions) (Discovery, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Discovery{}, fmt.Errorf("compose: resolve %s: %w", root, err)
	}
	phpRoot := o.PHPRoot
	if phpRoot == "" {
		phpRoot = abs
	} else if !filepath.IsAbs(phpRoot) {
		phpRoot = filepath.Join(abs, phpRoot)
	}
	d := Discovery{Root: abs, PHPRoot: filepath.Clean(phpRoot), Sets: []FileSet{}}
	for _, c := range candidateDirs(abs, o) {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		found, err := FindFiles(c.dir, o.Env)
		if errors.Is(err, ErrNoComposeFile) {
			continue
		}
		set := FileSet{Dir: c.dir, Relation: c.relation, Levels: c.levels, Candidates: []DiscoveredService{}}
		if err != nil {
			set.Error = err.Error()
			d.Sets = append(d.Sets, set)
			continue
		}
		set.Files = found.Paths
		set.Overlays = overlays(c.dir, found.Paths)
		p, err := Load(ctx, c.dir, LoadOptions{Env: o.Env, Files: found.Paths})
		if err != nil {
			set.Error = err.Error()
			d.Sets = append(d.Sets, set)
			continue
		}
		set.project = p
		set.Name, set.Services = p.Name, p.ServiceNames()
		set.Candidates = Rank(p, d.PHPRoot)
		d.Sets = append(d.Sets, set)
	}
	sort.SliceStable(d.Sets, func(i, j int) bool { return setScore(d.Sets[i]) > setScore(d.Sets[j]) })
	return d, nil
}

func setScore(s FileSet) int {
	if len(s.Candidates) == 0 {
		return -1 << 30
	}
	return s.Candidates[0].Score
}

type searchDir struct {
	dir      string
	relation string
	levels   int
}

// candidateDirs lists the directories to search: root, subfolders
// (breadth first, depth ≤ MaxDepth) and ancestors (up to MaxUp, never the
// home directory or the filesystem root).
func candidateDirs(root string, o DiscoverOptions) []searchDir {
	maxUp, maxDepth := o.MaxUp, o.MaxDepth
	if maxUp <= 0 {
		maxUp = 4
	}
	if maxDepth <= 0 {
		maxDepth = 2
	}
	home := o.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		home = filepath.Clean(home)
	}
	out := []searchDir{{dir: root, relation: RelationRoot}}
	level := []string{root}
	for depth := 1; depth <= maxDepth; depth++ {
		var next []string
		for _, dir := range level {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)] {
					continue
				}
				sub := filepath.Join(dir, name)
				next = append(next, sub)
				out = append(out, searchDir{dir: sub, relation: RelationSubdir, levels: depth})
			}
		}
		level = next
	}
	dir := root
	for up := 1; up <= maxUp; up++ {
		parent := filepath.Dir(dir)
		if parent == dir || filepath.Dir(parent) == parent || parent == home {
			break
		}
		out = append(out, searchDir{dir: parent, relation: RelationAncestor, levels: up})
		dir = parent
	}
	return out
}

// overlays lists further compose files in dir that are not in loaded:
// "<base>.<name>.yaml" variants of the standard names (also
// "docker-compose.override.arm.yaml"); *.dist files are templates and
// left out.
func overlays(dir string, loaded []string) []string {
	have := map[string]bool{}
	for _, p := range loaded {
		have[filepath.Clean(p)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		lower := strings.ToLower(n)
		if e.IsDir() || !(strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")) {
			continue
		}
		if !strings.HasPrefix(lower, "docker-compose.") && !strings.HasPrefix(lower, "compose.") {
			continue
		}
		p := filepath.Join(dir, n)
		if have[p] {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Rank scores every enabled service of p for the project whose PHP
// application lives in phpRoot and returns PHP services and services that
// mount the project, best first. On top of ScoreService it rewards bind
// mounts of the PHP root (most) or of a folder inside it, images built by
// another service of the file set (a "builder" service with the Dockerfile
// while the runtime service only names the image), and penalises one-shot
// services (entrypoint "true", setup/init names) and PHP services of a
// parent directory's compose file that do not mount the project at all.
func Rank(p *Project, phpRoot string) []DiscoveredService {
	if p == nil || p.Model == nil {
		return nil
	}
	phpRoot = filepath.Clean(phpRoot)
	built := builtImages(p)
	var out []DiscoveredService
	for _, name := range p.ServiceNames() {
		svc := p.Model.Services[name]
		c := ScoreService(p.Dir, name, svc)
		ds := DiscoveredService{Candidate: c}
		if is, _ := imageScore(svc.Image); is <= 0 && svc.Image != "" && svc.Build == nil {
			if b, ok := built[svc.Image]; ok && b.name != name {
				ds.Score += scoreBuiltImage
				ds.Reasons = append(ds.Reasons, fmt.Sprintf("image %q is built by service %q (%s)", svc.Image, b.name, b.reason))
			}
		}
		for _, pm := range c.Mappings {
			switch {
			case hasPrefix(phpRoot, pm.Host, string(filepath.Separator)):
				if ds.Mount != MountCovers {
					remote, _ := Mappings{pm}.ToRemote(phpRoot)
					m := pm
					ds.Mount, ds.ProjectRemote, ds.Mapping = MountCovers, remote, &m
				}
			case hasPrefix(pm.Host, phpRoot, string(filepath.Separator)) && ds.Mount == MountNone:
				m := pm
				ds.Mount, ds.Mapping = MountInside, &m
			}
		}
		switch ds.Mount {
		case MountCovers:
			if ds.Mapping.Host == phpRoot {
				ds.Score += scoreMountsPHPRoot
				ds.Reasons = append(ds.Reasons, fmt.Sprintf("mounts the PHP root %s at %s", phpRoot, ds.ProjectRemote))
			} else {
				ds.Score += scoreMountsInside
				ds.Reasons = append(ds.Reasons, fmt.Sprintf("mounts %s (contains the PHP root) at %s", ds.Mapping.Host, ds.Mapping.Remote))
			}
		case MountInside:
			ds.Score += scoreMountsInside
			ds.Reasons = append(ds.Reasons, fmt.Sprintf("mounts %s (inside the project) at %s", ds.Mapping.Host, ds.Mapping.Remote))
		default:
			// A compose file outside the PHP root serves several
			// applications: its PHP services that do not mount this one
			// belong to another application.
			if ds.Score > 0 && !hasPrefix(p.Dir, phpRoot, string(filepath.Separator)) {
				ds.Score += scoreUnrelatedMount
				ds.Reasons = append(ds.Reasons, "does not mount the project")
			}
		}
		if oneShot(svc) {
			ds.Score += scoreOneShot
			ds.Reasons = append(ds.Reasons, "one-shot service (entrypoint or command \"true\")")
		} else if taskName(name) {
			ds.Score += scoreTaskName
			ds.Reasons = append(ds.Reasons, fmt.Sprintf("service name %q suggests a setup task", name))
		}
		if ds.Score <= 0 && ds.Mount == MountNone {
			continue
		}
		if ds.Reasons == nil {
			ds.Reasons = []string{}
		}
		out = append(out, ds)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].Service < out[b].Service
	})
	return out
}

type builder struct{ name, reason string }

// builtImages maps image names to the service that builds them with PHP
// evidence (a Dockerfile FROM a PHP image).
func builtImages(p *Project) map[string]builder {
	out := map[string]builder{}
	for _, name := range p.ServiceNames() {
		svc := p.Model.Services[name]
		if svc.Image == "" || svc.Build == nil {
			continue
		}
		if s, why := buildScore(svc.Build, p.Dir); s > 0 && len(why) > 0 {
			out[svc.Image] = builder{name: name, reason: why[len(why)-1]}
		}
	}
	return out
}

func oneShot(svc types.ServiceConfig) bool {
	is := func(c types.ShellCommand) bool {
		return len(c) == 1 && (c[0] == "true" || c[0] == "/bin/true")
	}
	return is(svc.Entrypoint) || (len(svc.Entrypoint) == 0 && is(svc.Command))
}

func taskName(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"setup", "init", "builder", "migrat", "seed", "install"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// ResolveFiles returns the compose files of a project: configured files
// (absolute or relative to root; the first file's directory is the
// compose project directory) when given, else FindFiles(root).
func ResolveFiles(root string, configured []string, env map[string]string) (Files, error) {
	var paths []string
	for _, f := range configured {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !filepath.IsAbs(f) {
			f = filepath.Join(root, f)
		}
		f = filepath.Clean(f)
		if !isFile(f) {
			return Files{}, fmt.Errorf("compose: configured file %s: %w", f, os.ErrNotExist)
		}
		paths = append(paths, f)
	}
	if len(paths) == 0 {
		return FindFiles(root, env)
	}
	return Files{Dir: filepath.Dir(paths[0]), Paths: paths}, nil
}

// LoadResolved loads the project of ResolveFiles.
func LoadResolved(ctx context.Context, root string, configured []string, opts LoadOptions) (*Project, error) {
	f, err := ResolveFiles(root, configured, opts.Env)
	if err != nil {
		return nil, err
	}
	opts.Files = f.Paths
	return Load(ctx, f.Dir, opts)
}

// PHPImage reports whether an image reference names a PHP runtime image
// (official, well-known or named like one).
func PHPImage(ref string) bool {
	s, _ := imageScore(ref)
	return s > 0
}
