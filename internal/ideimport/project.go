package ideimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/phpver"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// IdeaDir is the project configuration directory of the reference IDE.
const IdeaDir = ".idea"

// HasProjectConfig reports whether dir contains a .idea directory.
func HasProjectConfig(dir string) bool { return isDir(filepath.Join(dir, IdeaDir)) }

// ProjectOptions configure ScanProject.
type ProjectOptions struct {
	// Home resolves $USER_HOME$.
	Home string
	// App is the application configuration (its interpreters resolve the
	// project's interpreter name); nil reads none.
	App *Source
	// ComposeEnv is passed to the compose loader (like Options.ComposeEnv
	// of the kernel).
	ComposeEnv map[string]string
}

// Mapping is a host/remote path mapping, stored like the kernel's
// MappingInfo.
type Mapping struct {
	Host   string `json:"host"`
	Remote string `json:"remote"`
}

// projectScan is the state of one ScanProject.
type projectScan struct {
	ctx    context.Context
	dir    string
	opts   ProjectOptions
	macros Macros
	plan   Plan
	// files are the parsed .idea/*.xml files (rel → root).
	files map[string]*Element
	order []string
	// remote is the project path inside the chosen Compose service.
	remote string
}

// coreProjectComponents are the project components ScanProject maps (or
// deliberately ignores) itself.
var coreProjectComponents = map[string]string{
	"PhpProjectSharedConfiguration":    "",
	"PhpInterpreters":                  "",
	"PhpProjectServersManager":         "",
	"PhpWorkspaceProjectConfiguration": "",
	"RemoteMappingsManager":            "",
	"RunManager":                       "",
	"ProjectRunConfigurationManager":   "",
	"ProjectModuleManager":             "module list, not needed",
	"ProjectRootManager":               "project SDK, not needed",
	"Encoding":                         "Tiegel reads and writes UTF-8",
}

// ScanProject reads dir/.idea and maps the interpreter (local, Compose
// service or Docker image), PHP language level, path mappings, code
// style and run configurations (PHP script; compound ones are reported).
// Everything else is returned as components for the plugins: the
// components of .idea/*.xml and the run configurations of other types.
func ScanProject(ctx context.Context, dir string, opts ProjectOptions) (Plan, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Plan{}, fmt.Errorf("ideimport: %w", err)
	}
	if !HasProjectConfig(abs) {
		return Plan{}, fmt.Errorf("ideimport: %s has no %s directory", abs, IdeaDir)
	}
	s := &projectScan{ctx: ctx, dir: abs, opts: opts, files: map[string]*Element{}}
	s.macros = Macros{ProjectDir: abs, UserHome: opts.Home}
	if opts.App != nil {
		s.macros.ConfigDir = opts.App.Dir
	}
	files, err := readXMLDir(abs, IdeaDir)
	if err != nil {
		return Plan{}, err
	}
	for _, f := range files {
		s.files[f.rel] = f.root
		s.order = append(s.order, f.rel)
	}
	s.interpreter()
	s.languageLevel()
	s.mappings()
	s.codeStyle()
	s.runConfigurations()
	for _, rel := range s.order {
		quiet := rel == IdeaDir+"/workspace.xml"
		for _, c := range s.files[rel].Components() {
			name := c.Attr("name")
			if reason, core := coreProjectComponents[name]; core {
				if reason != "" && !quiet {
					s.plan.add(Item{Area: AreaGeneral, Name: name, Source: rel, Status: StatusSkipped, Detail: reason})
				}
				continue
			}
			s.plan.Components = append(s.plan.Components, Component{
				Kind: KindComponent, Name: name, File: rel, Scope: ScopeProject,
				Element: c, Macros: s.macros, Quiet: quiet,
			})
		}
	}
	return s.plan, nil
}

// component returns the first component called name in the files rels
// (all files when none given) and its file.
func (s *projectScan) component(name string, rels ...string) (*Element, string) {
	if len(rels) == 0 {
		rels = s.order
	}
	for _, rel := range rels {
		if root := s.files[rel]; root != nil {
			if c := root.Component(name); c != nil {
				return c, rel
			}
		}
	}
	return nil, ""
}

// appInterpreters returns the application-wide interpreter list.
func (s *projectScan) appInterpreters() []*Element {
	if s.opts.App == nil {
		return nil
	}
	root, err := ParseFile(filepath.Join(s.opts.App.Dir, "options", "php.xml"))
	if err != nil {
		return nil
	}
	return root.Component("PhpInterpreters").Find("interpreter")
}

// Interpreter is the neutral form of an interpreter of the reference IDE.
type Interpreter struct {
	Name string `json:"name"`
	// Kind is "local", "compose", "docker" or the URL scheme of an
	// unsupported kind ("ssh", …).
	Kind string `json:"kind"`
	// Path is the PHP binary (local) or the path inside the container.
	Path string `json:"path,omitempty"`
	// Service, ComposeFiles: Compose interpreters; Image: Docker image.
	Service      string   `json:"service,omitempty"`
	ComposeFiles []string `json:"composeFiles,omitempty"`
	Image        string   `json:"image,omitempty"`
	// RemotePath is where the project is inside the container.
	RemotePath string `json:"remotePath,omitempty"`
	// Mode is the Compose start mode ("exec", "run" or "").
	Mode string `json:"mode,omitempty"`
}

var composeHomeRe = regexp.MustCompile(`^docker-compose://\[([^\]]*)\]:([^/]+)/(.*)$`)

// ParseInterpreter reads an <interpreter> element.
func ParseInterpreter(el *Element, m Macros) Interpreter {
	in := Interpreter{Name: el.Attr("name")}
	home := strings.TrimSpace(el.Attr("home"))
	rd := el.Child("remote_data")
	for _, a := range rd.attrsLike("LIFECYCLE") {
		switch strings.ToUpper(a) {
		case "EXEC", "CONNECT_TO_EXISTING_CONTAINER":
			in.Mode = "exec"
		case "RUN", "ALWAYS_START_NEW_CONTAINER":
			in.Mode = "run"
		}
	}
	in.RemotePath = rd.Attr("DOCKER_REMOTE_PROJECT_PATH")
	switch {
	case strings.HasPrefix(home, "docker-compose://"):
		in.Kind = "compose"
		if mm := composeHomeRe.FindStringSubmatch(home); mm != nil {
			for _, f := range strings.Split(mm[1], ",") {
				if p := m.Path(f); p != "" {
					in.ComposeFiles = append(in.ComposeFiles, p)
				}
			}
			in.Service, in.Path = mm[2], "/"+strings.TrimPrefix(mm[3], "/")
			if mm[3] == "php" {
				in.Path = "php"
			}
		}
		if v := rd.Attr("DOCKER_COMPOSE_SERVICE_NAME"); v != "" {
			in.Service = v
		}
		for _, it := range rd.Find("item") {
			if p := m.Path(it.Attr("value")); p != "" && !slices.Contains(in.ComposeFiles, p) {
				in.ComposeFiles = append(in.ComposeFiles, p)
			}
		}
	case strings.HasPrefix(home, "docker://"):
		in.Kind = "docker"
		rest := strings.TrimPrefix(home, "docker://")
		in.Image, in.Path = rest, "php"
		if i := strings.LastIndex(rest, "/"); i > 0 && !strings.Contains(rest[i:], ":") {
			in.Image, in.Path = rest[:i], rest[i+1:]
		}
		if v := rd.Attr("DOCKER_IMAGE_NAME"); v != "" {
			in.Image = v
		}
	case strings.Contains(home, "://"):
		in.Kind = home[:strings.Index(home, "://")]
		in.Path = home
	default:
		in.Kind, in.Path = "local", m.Path(home)
	}
	return in
}

// attrsLike returns the values of the attributes whose name contains sub.
func (e *Element) attrsLike(sub string) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, a := range e.Attrs {
		if strings.Contains(a.Name.Local, sub) {
			out = append(out, a.Value)
		}
	}
	return out
}

func (s *projectScan) interpreter() {
	ws, wsFile := s.component("PhpWorkspaceProjectConfiguration")
	name := ws.Attr("interpreter_name")
	if v, ok := ws.Option("interpreter_name"); ok && name == "" {
		name = v
	}
	projInterp, projFile := s.component("PhpInterpreters")
	candidates := projInterp.Find("interpreter")
	source := projFile
	var found *Element
	if name != "" {
		for _, el := range append(slices.Clone(candidates), s.appInterpreters()...) {
			if el.Attr("name") == name {
				found = el
				break
			}
		}
		source = wsFile
	} else if len(candidates) == 1 {
		found = candidates[0]
	}
	if found == nil {
		if name != "" {
			s.plan.add(Item{Area: AreaInterpreter, Name: "PHP interpreter", Source: wsFile, Status: StatusSkipped,
				Detail: fmt.Sprintf("interpreter %q is not defined in the imported configuration", name)})
		}
		return
	}
	in := ParseInterpreter(found, s.macros)
	it := Item{Area: AreaInterpreter, Name: "PHP interpreter " + in.Name, Source: source}
	switch in.Kind {
	case "local":
		if in.Path == "" {
			it.Status, it.Detail = StatusSkipped, "no path"
		} else if fi, err := os.Stat(in.Path); err != nil || fi.IsDir() {
			it.Status, it.Detail = StatusSkipped, in.Path+" does not exist here"
		} else {
			it.Detail = in.Path
			it.Changes = []Change{{Key: KeyInterpreter, Scope: ScopeProject, Value: "local:" + in.Path, Event: EventInterpreter}}
		}
	case "compose":
		s.composeInterpreter(&it, in)
	case "docker":
		remote := in.RemotePath
		if remote == "" {
			remote = "/opt/project"
		}
		it.Detail = fmt.Sprintf("Docker image %s, project at %s", in.Image, remote)
		it.Changes = []Change{
			{Key: KeyDockerImages, Scope: ScopeProject, Value: []string{in.Image + "=" + remote}, Merge: MergeList},
			{Key: KeyInterpreter, Scope: ScopeProject, Value: "docker:" + in.Image, Event: EventInterpreter},
		}
	default:
		it.Status, it.Detail = StatusUnsupported, fmt.Sprintf("%s interpreters are not supported (local, Compose and Docker image are)", in.Kind)
	}
	s.plan.add(it)
}

// composeInterpreter maps a Compose interpreter: the service of the
// project's compose files (as Tiegel loads them), start mode and the
// remote project path as mapping.
func (s *projectScan) composeInterpreter(it *Item, in Interpreter) {
	if in.Service == "" {
		it.Status, it.Detail = StatusSkipped, "no Compose service"
		return
	}
	proj, err := compose.Load(s.ctx, s.dir, compose.LoadOptions{Env: s.opts.ComposeEnv})
	if err != nil {
		it.Status, it.Detail = StatusSkipped, "Compose files: "+err.Error()
		if errors.Is(err, compose.ErrNoComposeFile) {
			it.Detail = "no Compose file in the project root"
		}
		return
	}
	if _, ok := proj.Service(in.Service); !ok {
		it.Status, it.Detail = StatusSkipped, fmt.Sprintf("service %q is not in the project's Compose files", in.Service)
		return
	}
	id := "compose:" + proj.Name + ":" + in.Service
	it.Detail = fmt.Sprintf("Compose service %s", in.Service)
	it.Changes = []Change{{Key: KeyInterpreter, Scope: ScopeProject, Value: id, Event: EventInterpreter}}
	if in.Mode != "" {
		it.Changes = append(it.Changes, Setting(KeyExecMode, ScopeProject, in.Mode))
		it.Detail += ", " + in.Mode
	}
	if len(in.ComposeFiles) > 0 && !sameFiles(in.ComposeFiles, proj.Files) {
		var rel []string
		for _, f := range proj.Files {
			rel = append(rel, s.macros.ProjectRel(f))
		}
		it.Detail += "; note: Tiegel uses the project's Compose files " + strings.Join(rel, ", ")
	}
	if in.RemotePath != "" {
		it.Detail += ", project at " + in.RemotePath
		s.remote = in.RemotePath
	}
}

func sameFiles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, f := range a {
		if !slices.ContainsFunc(b, func(g string) bool { return filepath.Clean(g) == filepath.Clean(f) }) {
			return false
		}
	}
	return true
}

func (s *projectScan) languageLevel() {
	c, file := s.component("PhpProjectSharedConfiguration")
	if c == nil {
		return
	}
	level := c.Attr("php_language_level")
	if level == "" {
		return
	}
	it := Item{Area: AreaInterpreter, Name: "PHP language level", Source: file}
	v, err := phpver.ParseVersion(level)
	if err != nil {
		it.Status, it.Detail = StatusUnsupported, "unknown level "+level
	} else {
		it.Detail = v.Short()
		it.Changes = []Change{{Key: KeyPHPVersionOverride, Scope: ScopeProject, Value: v.Short(), Event: EventPHPVersion}}
	}
	s.plan.add(it)
}

// mappings collects the path mappings of the servers (php.xml), the
// remote mappings and the remote path of a container interpreter.
func (s *projectScan) mappings() {
	var list []Mapping
	var sources []string
	add := func(local, remote, file string) {
		h := s.macros.Path(local)
		remote = strings.TrimSpace(remote)
		if h == "" || remote == "" {
			return
		}
		for _, m := range list {
			if m.Host == h {
				return
			}
		}
		list = append(list, Mapping{Host: h, Remote: remote})
		if !slices.Contains(sources, file) {
			sources = append(sources, file)
		}
	}
	if c, file := s.component("PhpProjectServersManager"); c != nil {
		for _, m := range c.Find("mapping") {
			add(m.Attr("local-root"), m.Attr("remote-root"), file)
		}
	}
	if c, file := s.component("RemoteMappingsManager"); c != nil {
		for _, m := range c.Find("mapping") {
			add(m.Attr("local-root"), m.Attr("remote-root"), file)
		}
	}
	// The remote project path of the chosen Compose interpreter.
	if s.remote != "" {
		add(s.dir, s.remote, IdeaDir+"/php.xml")
	}
	if len(list) == 0 {
		return
	}
	var parts []string
	for _, m := range list {
		parts = append(parts, s.macros.ProjectRel(m.Host)+" → "+m.Remote)
	}
	s.plan.add(Item{Area: AreaInterpreter, Name: "Path mappings", Source: strings.Join(sources, ", "),
		Detail: strings.Join(parts, ", "), Changes: []Change{{Key: KeyMappings, Scope: ScopeProject, Value: list, Event: EventMappings}}})
}

func (s *projectScan) codeStyle() {
	cfg, err := ParseFile(filepath.Join(s.dir, IdeaDir, "codeStyles", "codeStyleConfig.xml"))
	if err != nil {
		return
	}
	state := cfg.Find("state")
	perProject := false
	for _, st := range state {
		if v, ok := st.Option("USE_PER_PROJECT_SETTINGS"); ok {
			perProject, _ = parseBool(v)
		}
	}
	if !perProject {
		return
	}
	rel := IdeaDir + "/codeStyles/Project.xml"
	root, err := ParseFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	scheme := root.Find("code_scheme")
	if root.Name == "code_scheme" {
		scheme = []*Element{root}
	}
	if len(scheme) == 0 {
		return
	}
	cs := ParseCodeStyle(scheme[0])
	it := codeStyleItem(cs, "Project code style", rel, ScopeProject)
	// Tab width and spaces are global settings in Tiegel; per project
	// .editorconfig decides.
	var keep []Change
	for _, c := range it.Changes {
		if c.Key == settings.KeyRightMargin {
			keep = append(keep, c)
		}
	}
	if len(keep) < len(it.Changes) {
		it.Detail += "; indentation per project comes from .editorconfig"
	}
	it.Changes = keep
	if len(keep) == 0 && it.Status == "" {
		it.Status = StatusSkipped
	}
	s.plan.add(it)
}

// Run configuration types the core maps itself.
const (
	refTypePHPScript = "PhpLocalRunConfigurationType"
	refTypeCompound  = "CompoundRunConfigurationType"
)

func (s *projectScan) runConfigurations() {
	type cfg struct {
		el   *Element
		file string
	}
	var list []cfg
	if files, err := readXMLDir(s.dir, IdeaDir+"/runConfigurations"); err == nil {
		for _, f := range files {
			for _, c := range f.root.Find("configuration") {
				list = append(list, cfg{c, f.rel})
			}
		}
	}
	if rm, file := s.component("RunManager", IdeaDir+"/workspace.xml"); rm != nil {
		for _, c := range rm.All("configuration") {
			list = append(list, cfg{c, file})
		}
	}
	seen := map[string]bool{}
	for _, c := range list {
		name, typ := c.el.Attr("name"), c.el.Attr("type")
		if b, _ := parseBool(c.el.Attr("default")); b || name == "" {
			continue
		}
		if b, _ := parseBool(c.el.Attr("temporary")); b {
			continue
		}
		key := typ + "\x00" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		switch typ {
		case refTypePHPScript:
			s.plan.add(phpScriptConfig(c.el, c.file, s.macros))
		case refTypeCompound:
			var names []string
			for _, t := range c.el.All("toRun") {
				names = append(names, t.Attr("name"))
			}
			s.plan.add(Item{Area: AreaRun, Name: "Run configuration " + name, Source: c.file, Status: StatusUnsupported,
				Detail: "compound configurations are not supported; it runs " + strings.Join(names, ", ")})
		default:
			s.plan.Components = append(s.plan.Components, Component{
				Kind: KindRunConfiguration, Name: typ, File: c.file, Scope: ScopeProject, Element: c.el, Macros: s.macros,
			})
		}
	}
}

// RunEnv reads the environment variables of a run configuration
// (<envs><env name= value=/></envs> anywhere inside) as KEY=VALUE.
func RunEnv(el *Element) []string {
	var out []string
	for _, e := range el.Find("env") {
		if n := e.Attr("name"); n != "" {
			out = append(out, n+"="+e.Attr("value"))
		}
	}
	return out
}

// RunConfigItem returns the item of a run configuration that becomes cfg.
func RunConfigItem(c Component, cfg run.Config, detail string) Item {
	it := c.Item("Run configuration "+cfg.Name, detail, Change{RunConfig: &cfg})
	it.Area = AreaRun
	return it
}

func phpScriptConfig(el *Element, file string, m Macros) Item {
	c := Component{Kind: KindRunConfiguration, Name: refTypePHPScript, File: file, Scope: ScopeProject, Element: el, Macros: m}
	name := el.Attr("name")
	path := c.Path(el.Attr("path"))
	if path == "" {
		return Item{Area: AreaRun, Name: "Run configuration " + name, Source: file, Status: StatusSkipped, Detail: "no script file"}
	}
	vals := map[string]any{"file": path, "interpreter": run.InterpreterProject}
	if v := strings.TrimSpace(el.Attr("scriptParameters")); v != "" {
		vals["args"] = v
	}
	if cl := el.Child("CommandLine"); cl != nil {
		if v := strings.TrimSpace(cl.Attr("parameters")); v != "" {
			vals["phpOptions"] = v
		}
		if wd := c.Path(cl.Attr("workingDirectory")); wd != "" && wd != "." {
			vals["workingDir"] = wd
		}
	}
	if env := RunEnv(el); len(env) > 0 {
		vals["env"] = env
	}
	return RunConfigItem(c, run.Config{Name: name, Type: run.TypePHPScript, Values: vals}, "PHP script "+path)
}
