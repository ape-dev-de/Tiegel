package phpruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dockercli "github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// Compose discovery for interpreters (D520, D524): compose files above or
// below the project root are found (compose.discover), their services are
// offered as interpreter candidates (interp.list discover=true) and one
// user action (interp.useCompose, or interp.select of a discovered ID)
// stores the files in docker.composeFiles and selects the service; the
// path mappings follow from its bind mounts. Discovery reads local files
// only; no container is started and the engine is not contacted.

func (m *Module) registerDiscovery(p *api.Provider) {
	api.Add(p, "compose.discover", "Compose files around the project (root, subfolders, parent folders) with ranked PHP services and path mappings; local files only", m.composeDiscover)
	api.Add(p, "interp.useCompose", "Use a compose service as the project interpreter: stores the compose files (docker.composeFiles) and selects it", m.useCompose)
}

// DiscoverParams are the params of "compose.discover".
type DiscoverParams struct {
	// Profiles to activate when loading the files.
	Profiles []string `json:"profiles,omitempty"`
}

// DiscoveredSet is a compose file set of compose.discover.
type DiscoveredSet struct {
	Dir      string   `json:"dir"`
	Files    []string `json:"files"`
	Overlays []string `json:"overlays,omitempty"`
	// Relation is root, subdir or ancestor; Levels the folder distance.
	Relation string   `json:"relation"`
	Levels   int      `json:"levels"`
	Name     string   `json:"name,omitempty"`
	Services []string `json:"services,omitempty"`
	// Configured is true for the files the project uses now.
	Configured bool            `json:"configured"`
	Candidates []CandidateInfo `json:"candidates"`
	Error      string          `json:"error,omitempty"`
}

// DiscoverResult is the result of "compose.discover".
type DiscoverResult struct {
	Root    string          `json:"root"`
	PHPRoot string          `json:"phpRoot"`
	Sets    []DiscoveredSet `json:"sets"`
}

func (m *Module) discover(ctx context.Context, profiles []string) (compose.Discovery, error) {
	root := m.app.ProjectRoot()
	if root == "" {
		return compose.Discovery{}, api.Errorf(api.CodeNoProject, "no project open")
	}
	_ = profiles
	d, err := compose.Discover(ctx, root, compose.DiscoverOptions{PHPRoot: m.app.ToolRoot(ctx), Env: m.app.Options().ComposeEnv})
	if err != nil {
		return d, api.Errorf(api.CodeFailed, "compose discovery: %w", err)
	}
	return d, nil
}

func (m *Module) composeDiscover(ctx context.Context, p DiscoverParams) (DiscoverResult, error) {
	d, err := m.discover(ctx, p.Profiles)
	if err != nil {
		return DiscoverResult{}, err
	}
	var configured []string
	if proj, err := m.projectCompose(ctx, nil); err == nil {
		configured = proj.Files
	}
	res := DiscoverResult{Root: d.Root, PHPRoot: d.PHPRoot, Sets: []DiscoveredSet{}}
	for _, s := range d.Sets {
		ds := DiscoveredSet{Dir: s.Dir, Files: s.Files, Overlays: s.Overlays, Relation: s.Relation, Levels: s.Levels, Name: s.Name,
			Services: s.Services, Error: s.Error, Candidates: []CandidateInfo{}, Configured: reflect.DeepEqual(s.Files, configured)}
		for _, c := range s.Candidates {
			ds.Candidates = append(ds.Candidates, m.candidateInfo(s.Project(), c))
		}
		res.Sets = append(res.Sets, ds)
	}
	return res, nil
}

// candidateInfo converts a ranked service.
func (m *Module) candidateInfo(proj *compose.Project, c compose.DiscoveredService) CandidateInfo {
	ci := CandidateInfo{Service: c.Service, Score: c.Score, Reasons: c.Reasons, Image: c.Image, Mappings: toMappings(c.Mappings),
		ID: "compose:" + proj.Name + ":" + c.Service, Mount: c.Mount, ProjectRemote: c.ProjectRemote, XdebugHints: []HintInfo{}}
	for _, h := range composeHints(proj, c.Service, m.app.Options().GOOS) {
		// The hint checks the compose folder; the PHP root counts here.
		if h.Code == compose.HintProjectNotMapped && c.Mount == compose.MountCovers {
			continue
		}
		ci.XdebugHints = append(ci.XdebugHints, h)
	}
	if ci.Reasons == nil {
		ci.Reasons = []string{}
	}
	return ci
}

// detectDiscovered answers compose.detect without compose files in the
// project root with the best discovered file set.
func (m *Module) detectDiscovered(ctx context.Context, res ComposeResult) (ComposeResult, error) {
	d, err := m.discover(ctx, nil)
	if err != nil {
		return res, err
	}
	set, _, ok := d.Best()
	if !ok {
		for _, s := range d.Sets {
			if s.Error == "" {
				set, ok = s, true
				break
			}
		}
	}
	if !ok {
		return res, nil
	}
	res.Found, res.Source = true, "discovered"
	res.Dir, res.Name, res.Files, res.Services = set.Dir, set.Name, set.Files, set.Services
	res.Relation, res.Levels, res.Overlays = set.Relation, set.Levels, set.Overlays
	for _, c := range set.Candidates {
		res.Candidates = append(res.Candidates, m.candidateInfo(set.Project(), c))
	}
	return res, nil
}

// rankServices returns the PHP services of proj for interp.list: the
// ranked ones with a positive score and the selected service in any case.
func (m *Module) rankServices(ctx context.Context, proj *compose.Project, selected string) []compose.DiscoveredService {
	var out []compose.DiscoveredService
	has := false
	for _, c := range compose.Rank(proj, m.app.ToolRoot(ctx)) {
		id := "compose:" + proj.Name + ":" + c.Service
		if c.Score > 0 || id == selected {
			out = append(out, c)
			has = has || id == selected
		}
	}
	if svc, ok := strings.CutPrefix(selected, "compose:"+proj.Name+":"); ok && !has {
		if def, ok := proj.Service(svc); ok {
			out = append(out, compose.DiscoveredService{Candidate: compose.ScoreService(proj.Dir, svc, def)})
		}
	}
	return out
}

// discovered lists compose services and images found around the project
// that are not in found (configured).
func (m *Module) discovered(ctx context.Context, found map[string]interp.Interpreter) []InterpreterInfo {
	if m.app.ProjectRoot() == "" {
		return nil
	}
	d, err := m.discover(ctx, nil)
	if err != nil {
		return nil
	}
	var out []InterpreterInfo
	seen := map[string]bool{}
	for _, s := range d.Sets {
		for _, c := range s.Candidates {
			if c.Score <= 0 {
				continue
			}
			id := "compose:" + s.Name + ":" + c.Service
			if found[id] == nil && !seen[id] {
				seen[id] = true
				out = append(out, InterpreterInfo{ID: id, Label: fmt.Sprintf("PHP (compose %s/%s)", s.Name, c.Service), Kind: string(interp.KindCompose),
					Service: c.Service, Score: c.Score, Image: c.Image, Discovered: true, Files: s.Files, Dir: s.Dir, Relation: s.Relation,
					Reasons: c.Reasons, Mappings: toMappings(c.Mappings)})
			}
			if c.Image == "" || !compose.PHPImage(c.Image) {
				continue
			}
			iid := dockercli.ImageID(c.Image)
			if found[iid] == nil && !seen[iid] {
				seen[iid] = true
				remote := c.ProjectRemote
				if remote == "" {
					remote = "/app"
				}
				out = append(out, InterpreterInfo{ID: iid, Label: "Docker image " + c.Image, Kind: string(interp.KindDocker), Image: c.Image, Remote: remote,
					Discovered: true, Reasons: []string{fmt.Sprintf("image of compose service %q", c.Service)}})
			}
		}
	}
	return out
}

// configureDiscovered selects a discovered compose service by its ID:
// it configures the compose files that define it. orig is returned for
// other IDs.
func (m *Module) configureDiscovered(ctx context.Context, id string, orig error) (interp.Interpreter, error) {
	rest, ok := strings.CutPrefix(id, "compose:")
	if !ok || m.app.ProjectRoot() == "" {
		return nil, orig
	}
	name, service, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, orig
	}
	d, err := m.discover(ctx, nil)
	if err != nil {
		return nil, orig
	}
	for _, s := range d.Sets {
		if s.Name != name || s.Error != "" {
			continue
		}
		if _, ok := s.Project().Service(service); !ok {
			continue
		}
		if err := m.storeComposeFiles(s.Files); err != nil {
			return nil, err
		}
		return m.interpreter(ctx, id)
	}
	return nil, orig
}

// storeComposeFiles stores files (relative to the project root when
// below a common parent) in docker.composeFiles; the project root's own
// default files clear the setting.
func (m *Module) storeComposeFiles(files []string) error {
	root := m.app.ProjectRoot()
	if def, err := compose.FindFiles(root, m.app.Options().ComposeEnv); err == nil && reflect.DeepEqual(def.Paths, files) {
		if err := m.store().Delete(api.SettingComposeFiles); err != nil {
			return api.Errorf(api.CodeFailed, "compose files: %w", err)
		}
	} else {
		rel := make([]string, 0, len(files))
		for _, f := range files {
			if r, err := filepath.Rel(root, f); err == nil && filepath.IsAbs(f) {
				f = filepath.ToSlash(r)
			}
			rel = append(rel, f)
		}
		if err := settings.Set(m.store(), api.SettingComposeFiles, rel); err != nil {
			return api.Errorf(api.CodeFailed, "compose files: %w", err)
		}
	}
	m.mu.Lock()
	m.interps = map[string]interp.Interpreter{}
	m.mu.Unlock()
	return nil
}

// UseComposeParams are the params of "interp.useCompose".
type UseComposeParams struct {
	// Service is the compose service.
	Service string `json:"service"`
	// Files are the compose files (absolute or relative to the project
	// root); empty means the files compose.detect reports (configured,
	// project root or the best discovered set).
	Files []string `json:"files,omitempty"`
}

func (m *Module) useCompose(ctx context.Context, p UseComposeParams) (InterpreterInfo, error) {
	root := m.app.ProjectRoot()
	if root == "" {
		return InterpreterInfo{}, api.Errorf(api.CodeNoProject, "no project open")
	}
	if p.Service == "" {
		return InterpreterInfo{}, api.Errorf(api.CodeInvalidParams, "service is required")
	}
	files := p.Files
	if len(files) == 0 {
		det, err := m.composeDetect(ctx, ComposeParams{})
		if err != nil {
			return InterpreterInfo{}, err
		}
		if !det.Found {
			return InterpreterInfo{}, api.Errorf(api.CodeNotFound, "no compose file found around %s", root)
		}
		files = det.Files
	}
	proj, err := compose.LoadResolved(ctx, root, files, compose.LoadOptions{Env: m.app.Options().ComposeEnv})
	if err != nil {
		return InterpreterInfo{}, api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	if _, ok := proj.Service(p.Service); !ok {
		return InterpreterInfo{}, api.Errorf(api.CodeNotFound, "service %q is not defined in %s (have %s)", p.Service, strings.Join(proj.Files, ", "), strings.Join(proj.ServiceNames(), ", "))
	}
	if err := m.storeComposeFiles(proj.Files); err != nil {
		return InterpreterInfo{}, err
	}
	return m.interpSelect(ctx, InterpSelectParams{ID: "compose:" + proj.Name + ":" + p.Service})
}

// provideCandidates registers the candidates of php-runtime.interpreter.
func (m *Module) provideCandidates(ctx module.Context) error {
	return api.ProvideCandidates(ctx, api.CandidateProvider{
		Keys: []string{SettingInterpreter}, Source: "PHP binaries, compose services and Docker images around the project", Setup: true,
		Find: func(c context.Context, q api.SettingCandidatesQuery) ([]api.SettingCandidate, error) {
			if q.Root == "" {
				return nil, nil
			}
			list, err := m.interpList(c, InterpListParams{Discover: true})
			if err != nil {
				return nil, err
			}
			return interpreterCandidates(list), nil
		},
	})
}

// interpreterCandidates converts interp.list: the compose service with the
// highest score is recommended (it runs the project's PHP), else the first
// local binary.
func interpreterCandidates(list []InterpreterInfo) []api.SettingCandidate {
	best := -1
	for i, it := range list {
		if it.Kind == string(interp.KindCompose) && it.Score > 0 && (best < 0 || it.Score > list[best].Score) {
			best = i
		}
	}
	if best < 0 {
		for i, it := range list {
			if it.Kind == string(interp.KindLocal) {
				best = i
				break
			}
		}
	}
	out := make([]api.SettingCandidate, 0, len(list))
	for i, it := range list {
		var detail []string
		switch {
		case it.Path != "":
			detail = append(detail, it.Path)
		case len(it.Files) > 0:
			detail = append(detail, "compose: "+strings.Join(it.Files, ", "))
		}
		for _, mp := range it.Mappings {
			detail = append(detail, mp.Host+" → "+mp.Remote)
			break
		}
		if it.Discovered {
			detail = append(detail, "not configured yet")
		}
		label := it.Label
		if it.Version != "" && !strings.Contains(label, it.Version) {
			label += " " + it.Version
		}
		out = append(out, api.SettingCandidate{Value: it.ID, Label: label, Detail: strings.Join(detail, "; "), Recommended: i == best, Current: it.Selected})
	}
	return out
}

// SettingsPage implements module.PageProvider (D525).
func (m *Module) SettingsPage() (section, title string) { return "Project", "PHP Interpreter" }
