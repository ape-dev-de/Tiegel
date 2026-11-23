// Package phpruntime is the php-runtime module: PHP interpreters (local
// binaries, Docker Compose services, Docker images, WSL), version
// detection, the interpreter probe (version, extensions, Xdebug, ini
// files), host/container path mappings and the Xdebug setup diagnosis.
//
// API: interp.list/select/version/probe/status/addImage/removeImage,
// interp.useCompose, compose.detect, compose.discover, php.version,
// runtime.mappings.get/set, runtime.execMode,
// runtime.diagnose.
// Events: interpreter.changed (and interp.selected), runtime.mappingsChanged.
// Hooks provided: php.resolveVersion, runtime.pathMappings and
// run.interpreter filters, so other modules (lang-php, php-features,
// debug-xdebug, the run service) get the version, the mappings and the
// interpreter without calling this module. Hooks used: docker.endpoint
// (docker binary, context and PATH from the docker module; without it
// "docker" from PATH and docker's current context are used).
// Settings (D190, namespace php-runtime.*): see SettingSpecs; the keys
// used before (php.interpreter, runtime.execMode, runtime.mappings) are
// still read as fallback and replaced on the next write.
package phpruntime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dockercli "github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/phpver"
)

// ID is the module ID.
const ID = "php-runtime"

// Settings keys (project settings, global without a project).
const (
	// SettingInterpreter stores the selected interpreter ID.
	SettingInterpreter = api.SettingInterpreter
	// SettingMappings stores corrected path mappings ([]api.MappingInfo).
	SettingMappings = "php-runtime.mappings"
	// SettingExecMode stores the compose exec mode ("auto", "exec", "run").
	SettingExecMode = "php-runtime.execMode"
	// SettingImages lists the Docker image interpreters ("IMAGE" or
	// "IMAGE=REMOTE", remote default /app).
	SettingImages = "php-runtime.dockerImages"
	// SettingVersionOverride stores the PHP version override (set by
	// php-features with php.setVersionOverride).
	SettingVersionOverride = api.SettingPHPVersionOverride
)

// Keys used before the php-runtime namespace (D190); read as fallback.
const (
	legacyMappings = "runtime.mappings"
	legacyExecMode = "runtime.execMode"
)

// TopicInterpSelected is the legacy event of interp.select (InterpreterInfo).
const TopicInterpSelected = "interp.selected"

// hookTimeout bounds the hook handlers: a version query may start a
// container.
const hookTimeout = 60 * time.Second

// Options configure the module.
type Options struct {
	// Docker is the docker binary for compose and image interpreters
	// (tests use a fake script); it wins over the docker.endpoint hook.
	// Empty means the hook's binary, else "docker" from PATH.
	Docker string
}

// Module is the php-runtime module.
type Module struct {
	opts Options

	mu      sync.Mutex
	app     *api.App
	interps map[string]interp.Interpreter
	probes  *probeCache
}

// New creates the module.
func New(opts Options) *Module { return &Module{opts: opts} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string {
	return []string{"interp.*", "compose.*", "runtime.*", "php.version"}
}

// Start registers the API and the hooks.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("php-runtime: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.interps = map[string]interp.Interpreter{}
	m.probes = newProbeCache(app.ConfigDir())
	m.mu.Unlock()

	p := api.NewProvider(ctx)
	api.Add(p, "interp.list", "PHP interpreters: local binaries and compose services", m.interpList)
	api.Add(p, "interp.select", "Select the project's PHP interpreter (event interpreter.changed)", m.interpSelect)
	api.Add(p, "interp.version", "Query version (and optionally modules/ini) of an interpreter", m.interpVersion)
	api.Add(p, "compose.detect", "Compose PHP candidates with scores, reasons, path mappings and Xdebug hints", m.composeDetect)
	api.Add(p, "php.version", "Project PHP version with its source (override, interpreter, composer)", m.phpVersion)
	api.Add(p, "runtime.mappings.get", "Host/container path mappings (corrected or from compose)", m.mappingsGet)
	api.Add(p, "runtime.mappings.set", "Correct the path mappings (persisted; reset restores compose)", m.mappingsSet)
	api.Add(p, "runtime.execMode", "Get or set how PHP runs in compose services (auto, exec, run)", m.execMode)
	api.Add(p, "runtime.diagnose", "Interpreter, Xdebug ini values, mappings and setup hints", m.diagnose)
	api.Add(p, "interp.probe", "Interpreter info: version, extensions, Xdebug, ini files (cached; refresh re-reads)", m.interpProbe)
	api.Add(p, "interp.status", "Docker context and service state (running/stopped) of a compose or image interpreter", m.interpStatus)
	api.Add(p, "interp.addImage", "Add a Docker image interpreter (docker run --rm -v project:remote)", m.addImage)
	api.Add(p, "interp.removeImage", "Remove a Docker image interpreter", m.removeImage)
	m.registerDiscovery(p)
	if err := p.Err(); err != nil {
		return err
	}
	if err := m.provideCandidates(ctx); err != nil {
		return err
	}

	owner := hooks.WithOwner(ID)
	api.HookPHPVersion.On(ctx.Hooks(), 0, func(c context.Context, q api.PHPVersionQuery) (api.PHPVersionQuery, error) {
		if q.Version != "" {
			return q, nil
		}
		res, err := m.phpVersion(c, PHPVersionParams{})
		if err != nil {
			return q, nil // unknown: consumers use their defaults
		}
		return api.PHPVersionQuery{Version: res.Version, Short: res.Short, Source: res.Source, Detail: res.Detail}, nil
	}, owner, hooks.WithTimeout(hookTimeout))
	api.HookPathMappings.On(ctx.Hooks(), 0, func(c context.Context, q api.PathMappingsQuery) (api.PathMappingsQuery, error) {
		if len(q.Mappings) > 0 {
			return q, nil
		}
		res, err := m.mappingsGet(c, struct{}{})
		if err != nil {
			return q, nil
		}
		return api.PathMappingsQuery{Mappings: res.Mappings, Source: res.Source}, nil
	}, owner, hooks.WithTimeout(hookTimeout))
	api.HookRunInterpreter.On(ctx.Hooks(), 0, m.runInterpreter, owner, hooks.WithTimeout(hookTimeout))

	reset := func(bus.Event) {
		m.mu.Lock()
		m.interps = map[string]interp.Interpreter{}
		m.mu.Unlock()
	}
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectOpened, reset))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectClosed, reset))
	// A new docker context/binary or changed php-runtime settings (e.g.
	// from the settings dialog) invalidate the built interpreters.
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicDockerContextChanged, reset))
	ctx.OnStop(settings.Changed.Subscribe(ctx.Bus(), func(ch settings.Change) {
		if ch.Key == SettingExecMode || ch.Key == SettingImages || ch.Key == api.SettingComposeFiles {
			reset(bus.Event{})
		}
	}))
	return nil
}

// Stop implements module.Module; hooks and methods are removed by the
// kernel.
func (m *Module) Stop() error {
	m.mu.Lock()
	m.interps = nil
	m.mu.Unlock()
	return nil
}

func (m *Module) store() *settings.Store { return m.app.SettingsStore() }

// InterpreterInfo describes an interpreter for "interp.list".
type InterpreterInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Version  string `json:"version,omitempty"`
	Path     string `json:"path,omitempty"`
	Service  string `json:"service,omitempty"`
	Score    int    `json:"score,omitempty"`
	Selected bool   `json:"selected,omitempty"`
	// Image and Remote describe a Docker image interpreter.
	Image  string `json:"image,omitempty"`
	Remote string `json:"remote,omitempty"`
	// Context is the Docker context of compose and image interpreters
	// ("" = docker's current context).
	Context string `json:"context,omitempty"`
	// State is the compose service state (running, stopped, missing) or
	// "error"; only filled with interp.list status=true.
	State string `json:"state,omitempty"`
	// Discovered marks a candidate that is not configured yet (interp.list
	// discover=true): selecting it (interp.select or interp.useCompose)
	// stores its compose files first (D524).
	Discovered bool `json:"discovered,omitempty"`
	// Files, Dir and Relation describe the compose file set of a
	// discovered compose service; Reasons explain its rank.
	Files    []string `json:"files,omitempty"`
	Dir      string   `json:"dir,omitempty"`
	Relation string   `json:"relation,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
	// Mappings are the host/container mappings the interpreter would use.
	Mappings []api.MappingInfo `json:"mappings,omitempty"`
}

// InterpListParams are the params of "interp.list".
type InterpListParams struct {
	// Compose includes services of the project's compose file (default
	// true; set false to skip loading compose files).
	Compose *bool `json:"compose,omitempty"`
	// Status asks the engine for the state of compose services
	// (`docker compose ps`); off by default.
	Status bool `json:"status,omitempty"`
	// Discover adds compose services and Docker images found around the
	// project (parent folders, subfolders; local files only) that are not
	// configured yet, marked Discovered.
	Discover bool `json:"discover,omitempty"`
}

// InterpSelectParams are the params of "interp.select".
type InterpSelectParams struct {
	ID string `json:"id"`
}

// InterpVersionParams are the params of "interp.version".
type InterpVersionParams struct {
	// ID of the interpreter; empty means the selected one. "local:<path>"
	// works for any php binary, even if it was not discovered.
	ID      string `json:"id,omitempty"`
	Modules bool   `json:"modules,omitempty"`
	Ini     bool   `json:"ini,omitempty"`
}

// InterpVersionResult is the result of "interp.version".
type InterpVersionResult struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Modules []string          `json:"modules,omitempty"`
	Ini     map[string]string `json:"ini,omitempty"`
}

// execModeOf parses the stored exec mode.
func execModeOf(s string) (compose.ExecMode, error) {
	switch s {
	case "", "auto":
		return compose.ExecAuto, nil
	case "exec":
		return compose.ExecAlways, nil
	case "run":
		return compose.RunAlways, nil
	}
	return 0, fmt.Errorf("unknown exec mode %q (auto, exec, run)", s)
}

func (m *Module) composeInterpreter(ctx context.Context, proj *compose.Project, service string) *compose.Interpreter {
	ci := compose.NewInterpreter(proj, service)
	ep := m.dockerEndpoint(ctx)
	ci.Docker, ci.Context, ci.Env = ep.Binary, ep.Context, ep.Env
	ci.Mode, _ = execModeOf(m.execModeSetting())
	return ci
}

func (m *Module) loadCompose(ctx context.Context, dir string, profiles []string) (*compose.Project, error) {
	opts := m.app.Options()
	return compose.Load(ctx, dir, compose.LoadOptions{Env: opts.ComposeEnv, Profiles: profiles})
}

// projectCompose loads the compose project of the open project: the
// configured files (docker.composeFiles, D523) or the project root's.
func (m *Module) projectCompose(ctx context.Context, profiles []string) (*compose.Project, error) {
	root := m.app.ProjectRoot()
	if root == "" {
		return nil, compose.ErrNoComposeFile
	}
	return compose.LoadResolved(ctx, root, m.composeFiles(), compose.LoadOptions{Env: m.app.Options().ComposeEnv, Profiles: profiles})
}

// composeFiles returns the configured compose files.
func (m *Module) composeFiles() []string {
	return api.Setting(m.app, api.SettingComposeFiles, []string{})
}

func (m *Module) interpList(ctx context.Context, p InterpListParams) ([]InterpreterInfo, error) {
	selected := api.SelectedInterpreter(m.store())
	found := make(map[string]interp.Interpreter)
	out := []InterpreterInfo{}
	for _, l := range interp.Discover(ctx, m.app.Options().Interp) {
		found[l.ID()] = l
		out = append(out, InterpreterInfo{ID: l.ID(), Label: l.Label(), Kind: string(l.Kind()), Version: l.CachedVersion(), Path: l.Path})
	}
	// PHP inside WSL distributions (Windows only; a no-op elsewhere).
	wsls, err := interp.DiscoverWSL(ctx, interp.WSLOptions{})
	if err != nil {
		m.app.Logger().Warn("wsl", "err", err)
	}
	for _, w := range wsls {
		found[w.ID()] = w
		out = append(out, InterpreterInfo{ID: w.ID(), Label: w.Label(), Kind: string(w.Kind()), Path: w.Path})
	}
	if root := m.app.ProjectRoot(); root != "" && (p.Compose == nil || *p.Compose) {
		if proj, err := m.projectCompose(ctx, nil); err == nil {
			for _, c := range m.rankServices(ctx, proj, selected) {
				ci := m.composeInterpreter(ctx, proj, c.Service)
				found[ci.ID()] = ci
				info := InterpreterInfo{ID: ci.ID(), Label: ci.Label(), Kind: string(ci.Kind()), Service: c.Service, Score: c.Score, Image: c.Image, Context: ci.Context,
					Reasons: c.Reasons, Mappings: toMappings(ci.Mappings)}
				if p.Status {
					info.State = m.serviceState(ctx, ci)
				}
				out = append(out, info)
			}
		} else if !errors.Is(err, compose.ErrNoComposeFile) {
			m.app.Logger().Warn("compose", "err", err)
		}
	}
	for _, d := range m.imageInterpreters(ctx) {
		found[d.ID()] = d
		out = append(out, InterpreterInfo{ID: d.ID(), Label: d.Label(), Kind: string(d.Kind()), Image: d.Image, Remote: d.RemoteDir(), Context: d.Client.Context})
	}
	if p.Discover {
		out = append(out, m.discovered(ctx, found)...)
	}
	for i := range out {
		out[i].Selected = out[i].ID == selected
		if c, ok := m.cachedProbe(out[i].ID); ok && out[i].Version == "" {
			out[i].Version = c.Info.Version
		}
	}
	m.mu.Lock()
	m.interps = found
	m.mu.Unlock()
	return out, nil
}

// interpreter finds an interpreter by ID in the last listing (listing first
// if needed). "local:<path>" IDs are accepted for any binary.
func (m *Module) interpreter(ctx context.Context, id string) (interp.Interpreter, error) {
	m.mu.Lock()
	i, ok := m.interps[id]
	m.mu.Unlock()
	if ok {
		return i, nil
	}
	if path, isLocal := strings.CutPrefix(id, "local:"); isLocal && path != "" {
		if _, err := exec.LookPath(path); err != nil {
			return nil, api.Errorf(api.CodeNotFound, "interpreter %s: %w", id, err)
		}
		return interp.NewLocal(path), nil
	}
	if image, isDocker := strings.CutPrefix(id, dockercli.ImageIDPrefix); isDocker && image != "" {
		return m.imageInterpreter(ctx, image, ""), nil
	}
	if rest, isWSL := strings.CutPrefix(id, "wsl:"); isWSL {
		if distro, path, ok := strings.Cut(rest, ":"); ok && distro != "" && path != "" {
			return interp.NewWSL(distro, path), nil
		}
	}
	if _, err := m.interpList(ctx, InterpListParams{}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	i, ok = m.interps[id]
	m.mu.Unlock()
	if ok {
		return i, nil
	}
	return nil, api.Errorf(api.CodeNotFound, "unknown interpreter %q (see interp.list)", id)
}

// selected returns the selected interpreter, or nil if none is selected.
func (m *Module) selected(ctx context.Context) (interp.Interpreter, error) {
	id := api.SelectedInterpreter(m.store())
	if id == "" {
		return nil, nil
	}
	return m.interpreter(ctx, id)
}

func (m *Module) interpSelect(ctx context.Context, p InterpSelectParams) (InterpreterInfo, error) {
	if p.ID == "" {
		return InterpreterInfo{}, api.Errorf(api.CodeInvalidParams, "id is required")
	}
	i, err := m.interpreter(ctx, p.ID)
	if err != nil {
		// A discovered compose service: configure its files first (D524).
		if i, err = m.configureDiscovered(ctx, p.ID, err); err != nil {
			return InterpreterInfo{}, err
		}
	}
	prev := api.SelectedInterpreter(m.store())
	if err := m.setMigrated(api.SettingInterpreter, api.SettingInterpreterLegacy, i.ID()); err != nil {
		return InterpreterInfo{}, api.Errorf(api.CodeFailed, "saving selection: %w", err)
	}
	info := InterpreterInfo{ID: i.ID(), Label: i.Label(), Kind: string(i.Kind()), Selected: true}
	b := m.app.Bus()
	b.Publish(TopicInterpSelected, info)
	if prev != i.ID() {
		b.Publish(api.TopicInterpreterChanged, api.InterpreterChangedEvent{ID: i.ID(), Label: i.Label(), Kind: string(i.Kind()), Previous: prev})
	}
	return info, nil
}

func (m *Module) interpVersion(ctx context.Context, p InterpVersionParams) (InterpVersionResult, error) {
	id := p.ID
	if id == "" {
		id = api.SelectedInterpreter(m.store())
		if id == "" {
			return InterpVersionResult{}, api.Errorf(api.CodeInvalidParams, "no interpreter selected; pass id")
		}
	}
	i, err := m.interpreter(ctx, id)
	if err != nil {
		return InterpVersionResult{}, err
	}
	v, err := i.Version(ctx)
	if err != nil {
		return InterpVersionResult{}, api.Errorf(api.CodeUnavailable, "php version: %w", err)
	}
	res := InterpVersionResult{ID: i.ID(), Version: v}
	if p.Modules {
		if res.Modules, err = i.Modules(ctx); err != nil {
			return res, api.Errorf(api.CodeUnavailable, "php modules: %w", err)
		}
	}
	if p.Ini {
		ini, err := i.Ini(ctx)
		if err != nil {
			return res, api.Errorf(api.CodeUnavailable, "php ini: %w", err)
		}
		res.Ini = ini
	}
	return res, nil
}

// ComposeParams are the params of "compose.detect".
type ComposeParams struct {
	// Dir is the directory with the compose files; empty means the project
	// root.
	Dir      string   `json:"dir,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
}

// HintInfo is a diagnostic about the Xdebug setup.
type HintInfo struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// CandidateInfo is a compose service that probably runs PHP.
type CandidateInfo struct {
	Service     string            `json:"service"`
	Score       int               `json:"score"`
	Reasons     []string          `json:"reasons"`
	Image       string            `json:"image,omitempty"`
	Mappings    []api.MappingInfo `json:"mappings"`
	XdebugHints []HintInfo        `json:"xdebugHints"`
	// ID is the interpreter ID of the service.
	ID string `json:"id,omitempty"`
	// Mount (covers, inside or "") and ProjectRemote tell how the service
	// mounts the project's PHP root (D520).
	Mount         string `json:"mount,omitempty"`
	ProjectRemote string `json:"projectRemote,omitempty"`
}

// ComposeResult is the result of "compose.detect".
type ComposeResult struct {
	Found      bool            `json:"found"`
	Dir        string          `json:"dir"`
	Name       string          `json:"name,omitempty"`
	Files      []string        `json:"files,omitempty"`
	Services   []string        `json:"services,omitempty"`
	Candidates []CandidateInfo `json:"candidates"`
	// Source tells where the files come from: dir (the dir param),
	// project (project root), configured (docker.composeFiles) or
	// discovered (found around the project, not configured yet).
	Source string `json:"source,omitempty"`
	// Relation and Levels locate discovered files (ancestor, subdir).
	Relation string `json:"relation,omitempty"`
	Levels   int    `json:"levels,omitempty"`
	// Overlays are optional extra compose files next to the files.
	Overlays []string `json:"overlays,omitempty"`
}

func composeHints(proj *compose.Project, service, goos string) []HintInfo {
	out := []HintInfo{}
	for _, h := range compose.XdebugHints(proj, service, goos) {
		out = append(out, HintInfo{Severity: h.Severity.String(), Code: h.Code, Message: h.Message})
	}
	return out
}

func toMappings(ms compose.Mappings) []api.MappingInfo {
	out := []api.MappingInfo{}
	for _, mp := range ms {
		out = append(out, api.MappingInfo{Host: mp.Host, Remote: mp.Remote})
	}
	return out
}

func (m *Module) composeDetect(ctx context.Context, p ComposeParams) (ComposeResult, error) {
	dir := p.Dir
	if dir == "" {
		dir = m.app.ProjectRoot()
	}
	if dir == "" {
		return ComposeResult{}, api.Errorf(api.CodeNoProject, "no project open and no dir given")
	}
	abs, err := m.app.ResolvePath(dir)
	if err != nil {
		return ComposeResult{}, err
	}
	res := ComposeResult{Dir: abs, Candidates: []CandidateInfo{}, Source: "dir"}
	var proj *compose.Project
	if p.Dir == "" {
		proj, err = m.projectCompose(ctx, p.Profiles)
		res.Source = "project"
		if len(m.composeFiles()) > 0 {
			res.Source = "configured"
		}
	} else {
		proj, err = m.loadCompose(ctx, abs, p.Profiles)
	}
	if errors.Is(err, compose.ErrNoComposeFile) && p.Dir == "" {
		// Not in the project root: look around it (D520).
		return m.detectDiscovered(ctx, res)
	}
	if errors.Is(err, compose.ErrNoComposeFile) {
		return res, nil
	}
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "compose: %w", err)
	}
	res.Found = true
	res.Name, res.Files, res.Services, res.Dir = proj.Name, proj.Files, proj.ServiceNames(), proj.Dir
	if p.Dir != "" {
		for _, c := range compose.Detect(proj) {
			res.Candidates = append(res.Candidates, m.candidateInfo(proj, compose.DiscoveredService{Candidate: c}))
		}
		return res, nil
	}
	for _, c := range compose.Rank(proj, m.app.ToolRoot(ctx)) {
		res.Candidates = append(res.Candidates, m.candidateInfo(proj, c))
	}
	return res, nil
}

// PHPVersionParams are the params of "php.version".
type PHPVersionParams struct {
	// Override wins over every other source (e.g. "8.2"); without it the
	// stored override (php.setVersionOverride) applies.
	Override string `json:"override,omitempty"`
	// Interpreter is the interpreter ID to ask; empty means the selected
	// one. NoInterpreter skips asking an interpreter at all.
	Interpreter   string `json:"interpreter,omitempty"`
	NoInterpreter bool   `json:"noInterpreter,omitempty"`
	// Start allows starting a container of a compose interpreter
	// (`docker compose run`, which may pull an image) when the service is
	// not running. Only pass it for an explicit user action; by default
	// php.version only uses `exec` on a running container and otherwise
	// falls back to composer.json with the hint container-not-running.
	Start bool `json:"start,omitempty"`
}

// PHPVersionResult is the result of "php.version".
type PHPVersionResult struct {
	Version string `json:"version"`
	Short   string `json:"short"`
	// Source is where the version came from: override, interpreter,
	// platform (composer config.platform.php) or require (require.php).
	Source string `json:"source"`
	Detail string `json:"detail,omitempty"`
	// Hint explains a fallback, e.g. code container-not-running when the
	// selected compose service is stopped (the version then comes from
	// composer.json).
	Hint *HintInfo `json:"hint,omitempty"`
}

// HintContainerNotRunning is the hint code of php.version and
// runtime.diagnose when the selected compose service is not running and
// the probe did not start it.
const HintContainerNotRunning = "container-not-running"

// probeInterpreter returns i for a probe without start: compose
// interpreters are restricted to `exec` unless start is true (see
// compose.Interpreter.ExecOnly), image interpreters never pull (--pull
// never). The second result is the compose interpreter to ask for the
// running state when the probe fails. Image interpreters still run a
// container here, so only an explicit re-read may use it; automatic
// queries use automaticInterpreter.
func probeInterpreter(i interp.Interpreter, start bool) (interp.Interpreter, *compose.Interpreter) {
	if start {
		return i, nil
	}
	switch c := i.(type) {
	case *compose.Interpreter:
		return c.ExecOnly(), c
	case *dockercli.ImageInterpreter:
		return c.WithoutPull(), nil
	}
	return i, nil
}

// automaticInterpreter returns i for an automatic query (php.version and
// the version hook, runtime.diagnose without start): like
// probeInterpreter, but an image interpreter never starts a container at
// all (D250) – it answers from the probe cache (*cachedImage).
func (m *Module) automaticInterpreter(i interp.Interpreter, start bool) (interp.Interpreter, *compose.Interpreter) {
	if d, ok := i.(*dockercli.ImageInterpreter); ok && !start {
		e, cached := m.cachedProbe(d.ID())
		return &cachedImage{ImageInterpreter: d, entry: e, cached: cached}, nil
	}
	return probeInterpreter(i, start)
}

// stoppedHint returns the container-not-running hint if ci's service is
// known to be stopped.
func stoppedHint(ctx context.Context, ci *compose.Interpreter) *HintInfo {
	if ci == nil {
		return nil
	}
	if running, err := ci.Running(ctx); err != nil || running {
		return nil
	}
	return &HintInfo{"info", HintContainerNotRunning,
		fmt.Sprintf("container not running: compose service %q is stopped; start it for the exact PHP version (no container is started automatically)", ci.Service)}
}

// Runner adapts any interp.Interpreter to phpver.Runner. For a compose
// interpreter the version comes from inside the container (docker compose
// exec/run, see runtime.execMode).
type Runner struct{ I interp.Interpreter }

// RunPHP implements phpver.Runner.
func (r Runner) RunPHP(ctx context.Context, args ...string) ([]byte, error) {
	return interp.Run(ctx, r.I, args...)
}

func (m *Module) phpVersion(ctx context.Context, p PHPVersionParams) (PHPVersionResult, error) {
	opts := phpver.Options{ProjectDir: m.app.ProjectRoot()}
	override := p.Override
	if override == "" {
		override = settings.Get(m.store(), SettingVersionOverride, "")
	}
	if override != "" {
		v, err := phpver.ParseVersion(override)
		if err != nil {
			return PHPVersionResult{}, api.Errorf(api.CodeInvalidParams, "override: %w", err)
		}
		opts.Override = &v
	}
	var probed *compose.Interpreter
	var cached *cachedImage
	if !p.NoInterpreter && opts.Override == nil {
		id := p.Interpreter
		if id == "" {
			id = api.SelectedInterpreter(m.store())
		}
		if id != "" {
			i, err := m.interpreter(ctx, id)
			if err != nil {
				return PHPVersionResult{}, err
			}
			// Privacy: never start a container for this (often automatic)
			// query unless explicitly allowed (D250).
			i, probed = m.automaticInterpreter(i, p.Start)
			if c, ok := i.(*cachedImage); ok {
				cached = c
				opts.Runner = c
			} else {
				opts.Runner = Runner{i}
			}
		} else if locals := interp.Discover(ctx, m.app.Options().Interp); len(locals) > 0 {
			// No explicit choice: the concept puts the real interpreter
			// first, so fall back to the first discovered local binary.
			opts.Runner = Runner{locals[0]}
		}
	}
	res, err := phpver.Detect(ctx, opts)
	var hint *HintInfo
	if err != nil || res.Source != phpver.SourceInterpreter {
		if probed != nil {
			hint = stoppedHint(ctx, probed)
		} else if cached != nil {
			hint = cached.hint(ctx)
		}
	}
	if err != nil {
		if hint != nil {
			err = fmt.Errorf("%s: %w", hint.Message, err)
		}
		if errors.Is(err, phpver.ErrUnknown) {
			return PHPVersionResult{}, api.Errorf(api.CodeNotFound, "%w", err)
		}
		return PHPVersionResult{}, api.Errorf(api.CodeFailed, "php version: %w", err)
	}
	return PHPVersionResult{Version: res.Version.String(), Short: res.Version.Short(), Source: res.Source.String(), Detail: res.Detail, Hint: hint}, nil
}

// MappingsResult is the result of "runtime.mappings.get/set".
type MappingsResult struct {
	Mappings []api.MappingInfo `json:"mappings"`
	// Source is "settings" (corrected by the user), "compose" (bind mounts
	// of the selected or best compose service), "docker" (the project
	// volume of the selected image interpreter) or "none".
	Source string `json:"source"`
	// Service is the compose service the mappings were derived from.
	Service string `json:"service,omitempty"`
	// Image is the image interpreter the mappings were derived from.
	Image string `json:"image,omitempty"`
}

// MappingsSetParams are the params of "runtime.mappings.set".
type MappingsSetParams struct {
	Mappings []api.MappingInfo `json:"mappings"`
	// Reset deletes the stored mappings (compose mappings apply again).
	Reset bool `json:"reset,omitempty"`
}

func (m *Module) mappingsGet(ctx context.Context, _ struct{}) (MappingsResult, error) {
	for _, key := range []string{SettingMappings, legacyMappings} {
		if stored, ok, _ := settings.Lookup[[]api.MappingInfo](m.store(), key); ok {
			if stored == nil {
				stored = []api.MappingInfo{}
			}
			return MappingsResult{Mappings: stored, Source: "settings"}, nil
		}
	}
	res := MappingsResult{Mappings: []api.MappingInfo{}, Source: "none"}
	root := m.app.ProjectRoot()
	if root == "" {
		return res, nil
	}
	if image, ok := strings.CutPrefix(api.SelectedInterpreter(m.store()), dockercli.ImageIDPrefix); ok && image != "" {
		d := m.imageInterpreter(ctx, image, "")
		for _, mp := range d.Mappings() {
			res.Mappings = append(res.Mappings, api.MappingInfo{Host: mp.Host, Remote: mp.Remote})
		}
		res.Source, res.Image = "docker", image
		return res, nil
	}
	proj, err := m.projectCompose(ctx, nil)
	if err != nil {
		return res, nil
	}
	service := ""
	if id := api.SelectedInterpreter(m.store()); strings.HasPrefix(id, "compose:") {
		service = id[strings.LastIndex(id, ":")+1:]
	}
	if service == "" {
		if cands := compose.Rank(proj, m.app.ToolRoot(ctx)); len(cands) > 0 && cands[0].Score > 0 {
			service = cands[0].Service
		}
	}
	if svc, ok := proj.Service(service); ok {
		res.Mappings = toMappings(compose.ServiceMappings(proj.Dir, svc))
		res.Source = "compose"
		res.Service = service
	}
	return res, nil
}

func (m *Module) mappingsSet(ctx context.Context, p MappingsSetParams) (MappingsResult, error) {
	if p.Reset {
		for _, key := range []string{SettingMappings, legacyMappings} {
			if err := m.store().Delete(key); err != nil {
				return MappingsResult{}, api.Errorf(api.CodeFailed, "mappings: %w", err)
			}
		}
	} else {
		ms := make([]api.MappingInfo, 0, len(p.Mappings))
		for _, mp := range p.Mappings {
			if mp.Host == "" || mp.Remote == "" {
				return MappingsResult{}, api.Errorf(api.CodeInvalidParams, "mapping needs host and remote: %+v", mp)
			}
			host, err := m.app.ResolvePath(mp.Host)
			if err != nil {
				return MappingsResult{}, err
			}
			ms = append(ms, api.MappingInfo{Host: host, Remote: mp.Remote})
		}
		if err := m.setMigrated(SettingMappings, legacyMappings, ms); err != nil {
			return MappingsResult{}, api.Errorf(api.CodeFailed, "mappings: %w", err)
		}
	}
	res, err := m.mappingsGet(ctx, struct{}{})
	if err == nil {
		m.app.Bus().Publish(api.TopicMappingsChanged, api.MappingsChangedEvent{Mappings: res.Mappings, Source: res.Source})
	}
	return res, err
}

// ExecModeParams are the params of "runtime.execMode".
type ExecModeParams struct {
	// Mode, if set, changes the mode: auto (exec if the service runs, run
	// otherwise), exec (always docker compose exec), run (always run --rm).
	Mode string `json:"mode,omitempty"`
}

// ExecModeResult is the result of "runtime.execMode".
type ExecModeResult struct {
	Mode string `json:"mode"`
}

func (m *Module) execMode(_ context.Context, p ExecModeParams) (ExecModeResult, error) {
	if p.Mode != "" {
		if _, err := execModeOf(p.Mode); err != nil {
			return ExecModeResult{}, api.Errorf(api.CodeInvalidParams, "%w", err)
		}
		if err := m.setMigrated(SettingExecMode, legacyExecMode, p.Mode); err != nil {
			return ExecModeResult{}, api.Errorf(api.CodeFailed, "exec mode: %w", err)
		}
		m.mu.Lock()
		m.interps = map[string]interp.Interpreter{} // rebuild with the new mode
		m.mu.Unlock()
	}
	return ExecModeResult{Mode: m.execModeSetting()}, nil
}

// DiagnoseParams are the params of "runtime.diagnose".
type DiagnoseParams struct {
	// Interpreter to diagnose; empty means the selected one.
	Interpreter string `json:"interpreter,omitempty"`
	// Start allows starting a container of a compose interpreter
	// (`docker compose run`); by default only `exec` on a running
	// container is used (hint container-not-running otherwise).
	Start bool `json:"start,omitempty"`
}

// Diagnosis is the result of "runtime.diagnose".
type Diagnosis struct {
	Interpreter *InterpreterInfo `json:"interpreter,omitempty"`
	Version     string           `json:"version,omitempty"`
	// Ini holds xdebug.mode, xdebug.client_host, xdebug.client_port and
	// xdebug.start_with_request as reported by the interpreter.
	Ini      map[string]string `json:"ini,omitempty"`
	ExecMode string            `json:"execMode"`
	Mappings MappingsResult    `json:"mappings"`
	Hints    []HintInfo        `json:"hints"`
}

// Hint codes of runtime.diagnose (compose hints keep their own codes).
const (
	HintNoInterpreter    = "no-interpreter"
	HintInterpreterError = "interpreter-error"
	HintXdebugMissing    = "xdebug-not-loaded"
	HintXdebugNoDebug    = "xdebug-mode-without-debug"
	HintClientPort       = "client-port"
	HintClientHostLocal  = "client-host-loopback"
	HintStartWithRequest = "start-with-request"
)

// DefaultDebugPort is the port the debug listener uses by default.
const DefaultDebugPort = "9003"

func (m *Module) diagnose(ctx context.Context, p DiagnoseParams) (Diagnosis, error) {
	res := Diagnosis{Hints: []HintInfo{}, ExecMode: m.execModeSetting()}
	var err error
	if res.Mappings, err = m.mappingsGet(ctx, struct{}{}); err != nil {
		return res, err
	}
	var i interp.Interpreter
	if p.Interpreter != "" {
		if i, err = m.interpreter(ctx, p.Interpreter); err != nil {
			return res, err
		}
	} else if i, err = m.selected(ctx); err != nil {
		return res, err
	}
	if i == nil {
		res.Hints = append(res.Hints, HintInfo{"warning", HintNoInterpreter, "no interpreter selected (interp.select)"})
		return res, nil
	}
	res.Interpreter = &InterpreterInfo{ID: i.ID(), Label: i.Label(), Kind: string(i.Kind()), Selected: true}
	if ci, ok := i.(*compose.Interpreter); ok {
		res.Interpreter.Service = ci.Service
		if root := m.app.ProjectRoot(); root != "" {
			if proj, err := m.projectCompose(ctx, nil); err == nil {
				res.Hints = append(res.Hints, composeHints(proj, ci.Service, m.app.Options().GOOS)...)
			}
		}
	}
	if d, ok := i.(*dockercli.ImageInterpreter); ok {
		res.Interpreter.Image, res.Interpreter.Remote, res.Interpreter.Context = d.Image, d.RemoteDir(), d.Client.Context
	}
	i, probed := m.automaticInterpreter(i, p.Start)
	if res.Version, err = i.Version(ctx); err != nil {
		if h := stoppedHint(ctx, probed); h != nil {
			res.Hints = append(res.Hints, *h)
			return res, nil
		}
		if c, ok := i.(*cachedImage); ok {
			res.Hints = append(res.Hints, *c.hint(ctx))
			return res, nil
		}
		res.Hints = append(res.Hints, HintInfo{"warning", HintInterpreterError, fmt.Sprintf("interpreter does not answer: %v", err)})
		return res, nil
	}
	ini, err := i.Ini(ctx)
	if err != nil {
		res.Hints = append(res.Hints, HintInfo{"warning", HintInterpreterError, fmt.Sprintf("reading ini values: %v", err)})
		return res, nil
	}
	res.Ini = ini
	res.Hints = append(res.Hints, iniHints(ini, i.Kind())...)
	return res, nil
}

// iniHints checks the Xdebug ini values of an interpreter.
func iniHints(ini map[string]string, kind interp.Kind) []HintInfo {
	var out []HintInfo
	mode, loaded := ini["xdebug.mode"]
	if !loaded {
		return append(out, HintInfo{"warning", HintXdebugMissing, "Xdebug is not loaded in this interpreter"})
	}
	if !strings.Contains(mode, "debug") {
		out = append(out, HintInfo{"warning", HintXdebugNoDebug, fmt.Sprintf("xdebug.mode=%q does not enable step debugging; include \"debug\"", mode)})
	}
	if port := ini["xdebug.client_port"]; port != "" && port != DefaultDebugPort {
		out = append(out, HintInfo{"info", HintClientPort, fmt.Sprintf("xdebug.client_port=%s; start the listener on that port (debug.listen addr)", port)})
	}
	if host := ini["xdebug.client_host"]; (kind == interp.KindCompose || kind == interp.KindDocker) && isLoopback(host) {
		out = append(out, HintInfo{"warning", HintClientHostLocal, fmt.Sprintf("xdebug.client_host=%s points at the container itself; use host.docker.internal", host)})
	}
	if swr := ini["xdebug.start_with_request"]; swr == "no" {
		out = append(out, HintInfo{"info", HintStartWithRequest, "xdebug.start_with_request=no: sessions only start with xdebug_break() or a trigger"})
	}
	return out
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
