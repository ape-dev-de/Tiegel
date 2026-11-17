package phpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dockercli "github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// statusTimeout bounds `docker compose ps` / `docker image inspect` for
// interp.list status=true and interp.status.
const statusTimeout = 10 * time.Second

// SettingSpecs implements module.SettingsProvider (D190).
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingInterpreter, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "PHP interpreter",
			Help:     "Selected interpreter (see interp.list): local:<path>, compose:<project>:<service>, docker:<image> or wsl:<distro>:<path>.",
			ReadOnly: true, ReadOnlyHint: "choose the interpreter with interp.select (headless: interpreters select ID) or the interpreter picker"},
		{Key: SettingExecMode, Kind: settings.KindEnum, Default: "auto", Options: []string{"auto", "exec", "run"}, Scope: settings.ScopeProject,
			Label: "Compose: start PHP with", Group: "Docker",
			Help: "auto uses docker compose exec when the service runs and run --rm otherwise; exec and run force one of them."},
		{Key: SettingImages, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject,
			Label: "Docker image interpreters", Group: "Docker",
			Help: "IMAGE or IMAGE=REMOTE; the project is mounted at REMOTE (default /app) and PHP runs with docker run --rm."},
	}
}

// setMigrated stores v under key in the current store and removes the
// legacy key, so the old value no longer shadows nothing and is not read
// again.
func (m *Module) setMigrated(key, legacy string, v any) error {
	s := m.store()
	if err := settings.Set(s, key, v); err != nil {
		return err
	}
	return s.Delete(legacy)
}

// execModeSetting returns the stored exec mode (new key, then legacy).
func (m *Module) execModeSetting() string {
	s := m.store()
	if v, ok, err := settings.Lookup[string](s, SettingExecMode); ok && err == nil {
		return v
	}
	return settings.Get(s, legacyExecMode, "auto")
}

// dockerEndpoint asks the docker module (hook docker.endpoint) for binary,
// context and PATH. Options.Docker wins over the hook's binary.
func (m *Module) dockerEndpoint(ctx context.Context) api.DockerEndpointQuery {
	q, _ := api.HookDockerEndpoint.Apply(ctx, m.app.Hooks(), api.DockerEndpointQuery{})
	if m.opts.Docker != "" {
		q.Binary = m.opts.Docker
	}
	return q
}

func (m *Module) dockerClient(ctx context.Context) dockercli.Client {
	ep := m.dockerEndpoint(ctx)
	return dockercli.Client{Binary: ep.Binary, Context: ep.Context, Env: ep.Env}
}

// imageSpecs returns the configured image interpreters (image → remote),
// in order.
func (m *Module) imageSpecs() ([]string, map[string]string) {
	var order []string
	remotes := map[string]string{}
	for _, spec := range settings.Get(m.store(), SettingImages, []string{}) {
		image, remote, err := dockercli.ParseImageSpec(spec)
		if err != nil {
			m.app.Logger().Warn("php-runtime: docker image", "err", err)
			continue
		}
		if _, dup := remotes[image]; !dup {
			order = append(order, image)
		}
		remotes[image] = remote
	}
	return order, remotes
}

func (m *Module) imageInterpreters(ctx context.Context) []*dockercli.ImageInterpreter {
	order, remotes := m.imageSpecs()
	if len(order) == 0 {
		return nil
	}
	c := m.dockerClient(ctx)
	out := make([]*dockercli.ImageInterpreter, 0, len(order))
	for _, image := range order {
		out = append(out, dockercli.NewImageInterpreter(image, remotes[image], m.app.ProjectRoot(), c))
	}
	return out
}

// imageInterpreter builds the interpreter of image; remote "" takes the
// configured remote (or /app).
func (m *Module) imageInterpreter(ctx context.Context, image, remote string) *dockercli.ImageInterpreter {
	if remote == "" {
		_, remotes := m.imageSpecs()
		remote = remotes[image]
	}
	return dockercli.NewImageInterpreter(image, remote, m.app.ProjectRoot(), m.dockerClient(ctx))
}

// serviceState asks the engine for the state of a compose interpreter's
// service; failures are reported as "error".
func (m *Module) serviceState(ctx context.Context, ci *compose.Interpreter) string {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	state, _, err := ci.ServiceState(ctx)
	if err != nil {
		return "error"
	}
	return state
}

// HintImageNotPulled is the hint code when an image interpreter's image is
// not available locally and was not pulled (no user action).
const HintImageNotPulled = "image-not-pulled"

// HintVersionUnknown is the hint code when an automatic query needs the
// PHP version of an image interpreter that was never probed: no container
// is started without a user action (D250), "Re-read" (interp.probe
// refresh) runs it once.
const HintVersionUnknown = "version-unknown"

// errNotProbed is returned by a *cachedImage without a cached probe.
var errNotProbed = errors.New("PHP version unknown – Re-read (no container is started automatically)")

// imageHint explains why an automatic query of image interpreter d has no
// result: the image is missing locally (image-not-pulled, asked with
// `docker image ls`, which starts no container) or it was never probed
// (version-unknown).
func imageHint(ctx context.Context, d *dockercli.ImageInterpreter) *HintInfo {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	if exists, err := d.Client.ImageExists(ctx, d.Image); err == nil && !exists {
		return &HintInfo{"info", HintImageNotPulled,
			fmt.Sprintf("image %s is not available locally; it is not pulled automatically (start it explicitly to pull)", d.Image)}
	}
	return &HintInfo{"info", HintVersionUnknown,
		fmt.Sprintf("PHP version of image %s unknown – Re-read to run it once (no container is started automatically)", d.Image)}
}

// cachedImage is an image interpreter for automatic queries (D250): it
// answers Version, Modules and Ini from the probe cache and never runs a
// container; without a cached probe they fail with errNotProbed. It also
// serves as phpver.Runner for the version query.
type cachedImage struct {
	*dockercli.ImageInterpreter
	entry  ProbeEntry
	cached bool
}

var _ interp.Interpreter = (*cachedImage)(nil)

// Version implements interp.Interpreter from the cache.
func (c *cachedImage) Version(context.Context) (string, error) {
	if !c.cached || c.entry.Info.Version == "" {
		return "", errNotProbed
	}
	return c.entry.Info.Version, nil
}

// Modules implements interp.Interpreter from the cache.
func (c *cachedImage) Modules(ctx context.Context) ([]string, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}
	return append(append([]string(nil), c.entry.Info.Extensions...), c.entry.Info.ZendExtensions...), nil
}

// Ini implements interp.Interpreter from the cache.
func (c *cachedImage) Ini(ctx context.Context) (interp.IniValues, error) {
	if _, err := c.Version(ctx); err != nil {
		return nil, err
	}
	ini := interp.IniValues{}
	for k, v := range c.entry.Info.Ini {
		ini[k] = v
	}
	return ini, nil
}

// Command implements interp.Interpreter: it never runs a container.
func (c *cachedImage) Command(context.Context, ...string) (*exec.Cmd, error) {
	return nil, errNotProbed
}

// RunPHP implements phpver.Runner: it only answers the version query
// (`-r echo PHP_VERSION;`) from the cache.
func (c *cachedImage) RunPHP(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) != 2 || args[0] != "-r" || !strings.Contains(args[1], "PHP_VERSION") {
		return nil, errNotProbed
	}
	v, err := c.Version(ctx)
	return []byte(v), err
}

// hint explains a missing cached result (see imageHint).
func (c *cachedImage) hint(ctx context.Context) *HintInfo { return imageHint(ctx, c.ImageInterpreter) }

// ---- probe cache ------------------------------------------------------------

// ProbeEntry is a cached probe result.
type ProbeEntry struct {
	Info     interp.Info `json:"info"`
	ProbedAt time.Time   `json:"probedAt"`
}

// probeCache keeps probe results per interpreter ID, persisted in
// <config>/cache/interp-probe.json so that the UI shows them without
// running PHP (or a container) on every start.
type probeCache struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	entries map[string]ProbeEntry
}

func newProbeCache(configDir string) *probeCache {
	pc := &probeCache{entries: map[string]ProbeEntry{}}
	if configDir != "" {
		pc.path = filepath.Join(configDir, "cache", "interp-probe.json")
	}
	return pc
}

func (pc *probeCache) loadLocked() {
	if pc.loaded || pc.path == "" {
		return
	}
	pc.loaded = true
	data, err := os.ReadFile(pc.path)
	if err != nil {
		return
	}
	var entries map[string]ProbeEntry
	if json.Unmarshal(data, &entries) == nil && entries != nil {
		pc.entries = entries
	}
}

func (pc *probeCache) get(id string) (ProbeEntry, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.loadLocked()
	e, ok := pc.entries[id]
	return e, ok
}

func (pc *probeCache) put(id string, e ProbeEntry) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.loadLocked()
	pc.entries[id] = e
	if pc.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(pc.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("probe cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pc.path), 0o755); err != nil {
		return fmt.Errorf("probe cache: %w", err)
	}
	tmp := pc.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("probe cache: %w", err)
	}
	if err := os.Rename(tmp, pc.path); err != nil {
		return fmt.Errorf("probe cache: %w", err)
	}
	return nil
}

func (m *Module) cachedProbe(id string) (ProbeEntry, bool) {
	m.mu.Lock()
	pc := m.probes
	m.mu.Unlock()
	if pc == nil {
		return ProbeEntry{}, false
	}
	return pc.get(id)
}

// ---- interp.probe -------------------------------------------------------------

// ProbeParams are the params of "interp.probe".
type ProbeParams struct {
	// ID of the interpreter; empty means the selected one.
	ID string `json:"id,omitempty"`
	// Refresh re-reads the info even if it is cached ("re-read", a user
	// action): only then (or with Start) an image interpreter runs a
	// container (D250).
	Refresh bool `json:"refresh,omitempty"`
	// Start allows starting a compose container (`run`) or pulling an
	// image; only for an explicit user action.
	Start bool `json:"start,omitempty"`
	// CachedOnly never runs PHP: without a cached result the method fails
	// with CodeNotFound (the UI uses it on open).
	CachedOnly bool `json:"cachedOnly,omitempty"`
}

// ProbeResult is the result of "interp.probe".
type ProbeResult struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`
	Info     interp.Info `json:"info"`
	Cached   bool        `json:"cached"`
	ProbedAt time.Time   `json:"probedAt"`
}

func kindOfID(id string) string {
	if k, _, ok := strings.Cut(id, ":"); ok {
		return k
	}
	return ""
}

func (m *Module) interpProbe(ctx context.Context, p ProbeParams) (ProbeResult, error) {
	id := p.ID
	if id == "" {
		if id = api.SelectedInterpreter(m.store()); id == "" {
			return ProbeResult{}, api.Errorf(api.CodeInvalidParams, "no interpreter selected; pass id")
		}
	}
	if !p.Refresh {
		if e, ok := m.cachedProbe(id); ok {
			return ProbeResult{ID: id, Kind: kindOfID(id), Info: e.Info, Cached: true, ProbedAt: e.ProbedAt}, nil
		}
	}
	if p.CachedOnly {
		return ProbeResult{}, api.Errorf(api.CodeNotFound, "no cached info for %s (probe with refresh)", id)
	}
	i, err := m.interpreter(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	// Only an explicit re-read (refresh) or start may run a container of
	// an image (D250).
	if d, ok := i.(*dockercli.ImageInterpreter); ok && !p.Refresh && !p.Start {
		h := imageHint(ctx, d)
		return ProbeResult{}, api.Errorf(api.CodeUnavailable, "%s", h.Message).WithData(h)
	}
	pi, probed := probeInterpreter(i, p.Start)
	info, err := interp.Probe(ctx, pi)
	if err != nil {
		if h := stoppedHint(ctx, probed); h != nil {
			return ProbeResult{}, api.Errorf(api.CodeUnavailable, "%s: %w", h.Message, err).WithData(h)
		}
		if d, ok := i.(*dockercli.ImageInterpreter); ok && !p.Start {
			if h := imageHint(ctx, d); h.Code == HintImageNotPulled {
				return ProbeResult{}, api.Errorf(api.CodeUnavailable, "%s: %w", h.Message, err).WithData(h)
			}
		}
		return ProbeResult{}, api.Errorf(api.CodeUnavailable, "probe %s: %w", id, err)
	}
	e := ProbeEntry{Info: info, ProbedAt: time.Now().UTC().Truncate(time.Second)}
	m.mu.Lock()
	pc := m.probes
	m.mu.Unlock()
	if pc != nil {
		if err := pc.put(i.ID(), e); err != nil {
			m.app.Logger().Warn("php-runtime: probe cache", "err", err)
		}
	}
	return ProbeResult{ID: i.ID(), Kind: string(i.Kind()), Info: info, ProbedAt: e.ProbedAt}, nil
}

// ---- interp.status ------------------------------------------------------------

// StatusParams are the params of "interp.status".
type StatusParams struct {
	// ID of the interpreter; empty means the selected one.
	ID string `json:"id,omitempty"`
}

// StatusResult is the result of "interp.status".
type StatusResult struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Context is the Docker context ("" = docker's current one).
	Context string `json:"context,omitempty"`
	// Service and State describe a compose interpreter (running, stopped,
	// missing); Containers are its containers.
	Service    string                   `json:"service,omitempty"`
	State      string                   `json:"state,omitempty"`
	Containers []compose.ContainerState `json:"containers,omitempty"`
	// Image and ImagePresent describe an image interpreter.
	Image        string `json:"image,omitempty"`
	ImagePresent *bool  `json:"imagePresent,omitempty"`
	// Error tells why the engine could not be asked.
	Error string `json:"error,omitempty"`
}

func (m *Module) interpStatus(ctx context.Context, p StatusParams) (StatusResult, error) {
	id := p.ID
	if id == "" {
		if id = api.SelectedInterpreter(m.store()); id == "" {
			return StatusResult{}, api.Errorf(api.CodeInvalidParams, "no interpreter selected; pass id")
		}
	}
	i, err := m.interpreter(ctx, id)
	if err != nil {
		return StatusResult{}, err
	}
	res := StatusResult{ID: i.ID(), Kind: string(i.Kind())}
	sctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	switch c := i.(type) {
	case *compose.Interpreter:
		res.Context, res.Service = c.Context, c.Service
		state, containers, err := c.ServiceState(sctx)
		if err != nil {
			res.State, res.Error = "error", err.Error()
		} else {
			res.State, res.Containers = state, containers
		}
	case *dockercli.ImageInterpreter:
		res.Context, res.Image = c.Client.Context, c.Image
		exists, err := c.Client.ImageExists(sctx, c.Image)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.ImagePresent = &exists
		}
	}
	return res, nil
}

// ---- interp.addImage / removeImage ---------------------------------------------

// ImageParams are the params of "interp.addImage" and "interp.removeImage".
type ImageParams struct {
	// Image is the image reference, e.g. "php:8.3-cli".
	Image string `json:"image"`
	// Remote is the container directory of the project (default /app).
	Remote string `json:"remote,omitempty"`
}

func (m *Module) saveImages(specs []string) error {
	if err := settings.Set(m.store(), SettingImages, specs); err != nil {
		return api.Errorf(api.CodeFailed, "docker images: %w", err)
	}
	m.mu.Lock()
	m.interps = map[string]interp.Interpreter{}
	m.mu.Unlock()
	return nil
}

func (m *Module) addImage(ctx context.Context, p ImageParams) (InterpreterInfo, error) {
	image, remote, err := dockercli.ParseImageSpec(dockercli.FormatImageSpec(strings.TrimSpace(p.Image), strings.TrimSpace(p.Remote)))
	if err != nil || strings.Contains(p.Image, "=") {
		return InterpreterInfo{}, api.Errorf(api.CodeInvalidParams, "image: %v", err)
	}
	order, remotes := m.imageSpecs()
	remotes[image] = remote
	specs := []string{}
	seen := false
	for _, im := range order {
		seen = seen || im == image
		specs = append(specs, dockercli.FormatImageSpec(im, remotes[im]))
	}
	if !seen {
		specs = append(specs, dockercli.FormatImageSpec(image, remote))
	}
	if err := m.saveImages(specs); err != nil {
		return InterpreterInfo{}, err
	}
	d := m.imageInterpreter(ctx, image, remote)
	return InterpreterInfo{ID: d.ID(), Label: d.Label(), Kind: string(d.Kind()), Image: image, Remote: d.RemoteDir(), Context: d.Client.Context,
		Selected: api.SelectedInterpreter(m.store()) == d.ID()}, nil
}

func (m *Module) removeImage(_ context.Context, p ImageParams) (struct{}, error) {
	image := strings.TrimSpace(p.Image)
	order, remotes := m.imageSpecs()
	specs := []string{}
	found := false
	for _, im := range order {
		if im == image {
			found = true
			continue
		}
		specs = append(specs, dockercli.FormatImageSpec(im, remotes[im]))
	}
	if !found {
		return struct{}{}, api.Errorf(api.CodeNotFound, "no image interpreter %q", image)
	}
	return struct{}{}, m.saveImages(specs)
}
