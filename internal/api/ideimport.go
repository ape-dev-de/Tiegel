package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Kernel service "import.ide" (M16, decisions D191, D390ff.): import the
// settings of the reference IDE. import.ide.sources lists the
// configuration directories, import.ide.scan previews an import (nothing
// is written) and import.ide.apply writes the selected areas. Reading
// happens only on these calls (user action), only locally.
//
// The core maps its own settings (internal/ideimport); every other
// component is offered to the plugins through HookIDEImportComponent.
// The kernel validates all changes (settings specs, run configuration
// types) and writes them; plugins never write during an import.

// IDEImportOptions configure the import (tests point them at fixtures).
type IDEImportOptions struct {
	// Roots are the vendor directories searched for configurations
	// (empty: the OS default below the user's home).
	Roots []string
	// Home is the user's home ($USER_HOME$, default roots). When it is
	// set, AppData and XDGConfig are taken from here as well; empty means
	// os.UserHomeDir and the environment (%APPDATA%, $XDG_CONFIG_HOME).
	Home      string
	AppData   string
	XDGConfig string
}

// IDEComponentQuery is the value of HookIDEImportComponent: a component
// of the reference IDE's configuration. A plugin that owns it appends
// its items and sets Handled (use OnIDEComponent).
type IDEComponentQuery struct {
	Component ideimport.Component
	Items     []ideimport.Item
	Handled   bool
}

// HookIDEImportComponent offers each component the core does not map to
// the plugins ("import.ide.component", D191).
var HookIDEImportComponent = hooks.NewFilter[IDEComponentQuery]("import.ide.component")

// OnIDEComponent registers fn for HookIDEImportComponent on behalf of the
// module of ctx: fn returns the items for a component it owns and true,
// or false to leave it to others. The items get the module as Plugin;
// a nil or empty list with true marks the component as handled without
// a report entry. The returned function removes the hook (it is removed
// with the module anyway).
func OnIDEComponent(ctx module.Context, fn func(c ideimport.Component) ([]ideimport.Item, bool)) func() {
	id := ctx.ModuleID()
	return HookIDEImportComponent.On(ctx.Hooks(), 100, func(_ context.Context, q IDEComponentQuery) (IDEComponentQuery, error) {
		items, ok := fn(q.Component)
		if !ok {
			return q, nil
		}
		out := slices.Clone(q.Items)
		for _, it := range items {
			if it.Plugin == "" {
				it.Plugin = id
			}
			if it.Status == "" {
				it.Status = ideimport.StatusReady
			}
			out = append(out, it)
		}
		q.Items, q.Handled = out, true
		return q, nil
	}, hooks.WithOwner(id))
}

// TopicIDEImported: an import wrote settings (IDEImportReport without
// items); clients reload settings, keymap and run configurations.
const TopicIDEImported = "import.ide.imported"

// IDEImportParams are the params of "import.ide.scan" and
// "import.ide.apply".
type IDEImportParams struct {
	// From is the application configuration directory ("": the newest
	// found, preferring one with a PHP configuration).
	From string `json:"from,omitempty"`
	// NoApp skips the application configuration, NoProject the open
	// project's .idea directory.
	NoApp     bool `json:"noApp,omitempty"`
	NoProject bool `json:"noProject,omitempty"`
	// Areas selects the areas (see ideimport.Areas); empty: the default
	// areas (all except recent projects).
	Areas []string `json:"areas,omitempty"`
	// DryRun makes import.ide.apply a preview.
	DryRun bool `json:"dryRun,omitempty"`
	// AllowProjectExecutables opts in to items that make Tiegel run a
	// program named by the project's .idea or lying inside the project
	// (interpreter, tool paths, runner, external tools; Item.Executable).
	// Without it they are skipped (D440).
	AllowProjectExecutables bool `json:"allowProjectExecutables,omitempty"`
}

// IDEImportArea is an area of the report.
type IDEImportArea struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`
	// Default: selected without a choice (recent projects are opt-in).
	Default bool `json:"default"`
	// Count is the number of items that would be or were imported.
	Count int `json:"count"`
	Total int `json:"total"`
}

// IDEImportCounts counts the items per status.
type IDEImportCounts struct {
	Ready       int `json:"ready"`
	Imported    int `json:"imported"`
	Skipped     int `json:"skipped"`
	Unsupported int `json:"unsupported"`
	Failed      int `json:"failed"`
}

// IDEImportReport is the result of "import.ide.scan" and
// "import.ide.apply".
type IDEImportReport struct {
	DryRun  bool              `json:"dryRun"`
	Source  *ideimport.Source `json:"source,omitempty"`
	Project string            `json:"project,omitempty"`
	Areas   []IDEImportArea   `json:"areas"`
	Items   []ideimport.Item  `json:"items"`
	Counts  IDEImportCounts   `json:"counts"`
	// Notes explain what was not looked at (no configuration found, no
	// .idea directory) and where the backup went.
	Notes []string `json:"notes,omitempty"`
	// Backup is the file with the global settings as they were before
	// the import changed them (D441); empty when no global setting was
	// written.
	Backup string `json:"backup,omitempty"`
}

// IDEImportStatus is the result of "import.ide.status".
type IDEImportStatus struct {
	// Project is the open project; ProjectConfig reports its .idea
	// directory (the GUI offers the import once).
	Project       string `json:"project,omitempty"`
	ProjectConfig bool   `json:"projectConfig"`
}

// IDEImportSourcesParams are the params of "import.ide.sources".
type IDEImportSourcesParams struct {
	// Dir lists only this directory (validated) instead of searching.
	Dir string `json:"dir,omitempty"`
}

func (a *App) registerIDEImport() {
	mustHandle(a, "import.ide.status", "Whether the open project has a configuration of the reference IDE (.idea); reads nothing else", a.ideImportStatus)
	mustHandle(a, "import.ide.sources", "Configuration directories of the reference IDE on this computer (newest first; read only on this call)", a.ideImportSources)
	mustHandle(a, "import.ide.scan", "Preview a settings import from the reference IDE (application configuration and the project's .idea): report per item, nothing is written", a.ideImportScan)
	mustHandle(a, "import.ide.apply", "Import settings from the reference IDE for the selected areas (dryRun: preview) and report imported / skipped / unsupported", a.ideImportApply)
}

func (a *App) ideHome() string {
	if h := a.opts.IDEImport.Home; h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func (a *App) ideDiscover() ideimport.DiscoverOptions {
	o := a.opts.IDEImport
	d := ideimport.DiscoverOptions{Roots: o.Roots, GOOS: a.opts.GOOS, Home: a.ideHome(), AppData: o.AppData, XDGConfig: o.XDGConfig}
	if o.Home == "" {
		d.AppData, d.XDGConfig = os.Getenv("APPDATA"), os.Getenv("XDG_CONFIG_HOME")
	}
	return d
}

func (a *App) ideImportSources(_ context.Context, p IDEImportSourcesParams) ([]ideimport.Source, error) {
	if p.Dir != "" {
		s, err := ideimport.SourceAt(p.Dir)
		if err != nil {
			return nil, Errorf(CodeInvalidParams, "%w", err)
		}
		return []ideimport.Source{s}, nil
	}
	list, err := ideimport.FindSources(a.ideDiscover())
	if err != nil {
		a.log.Warn("import sources", "err", err)
	}
	if list == nil {
		list = []ideimport.Source{}
	}
	return list, nil
}

func (a *App) ideImportStatus(context.Context, struct{}) (IDEImportStatus, error) {
	root := a.ProjectRoot()
	return IDEImportStatus{Project: root, ProjectConfig: root != "" && ideimport.HasProjectConfig(root)}, nil
}

func (a *App) ideImportScan(ctx context.Context, p IDEImportParams) (IDEImportReport, error) {
	p.DryRun = true
	return a.ideImport(ctx, p)
}

func (a *App) ideImportApply(ctx context.Context, p IDEImportParams) (IDEImportReport, error) {
	return a.ideImport(ctx, p)
}

// ideImport scans, offers components to the plugins, checks every
// change and (without DryRun) writes the selected items.
func (a *App) ideImport(ctx context.Context, p IDEImportParams) (IDEImportReport, error) {
	rep := IDEImportReport{DryRun: p.DryRun, Items: []ideimport.Item{}}
	selected, err := ideSelectedAreas(p.Areas)
	if err != nil {
		return rep, err
	}
	// Plans remember their origin: the application configuration is the
	// user's own (global); a project's .idea may come from a cloned
	// repository (project) and is trusted less (D440).
	type originPlan struct {
		plan   ideimport.Plan
		origin string
	}
	var plans []originPlan
	var src *ideimport.Source
	if !p.NoApp {
		if p.From != "" {
			s, err := ideimport.SourceAt(p.From)
			if err != nil {
				return rep, Errorf(CodeInvalidParams, "%w", err)
			}
			src = &s
		} else {
			list, _ := ideimport.FindSources(a.ideDiscover())
			if s, ok := ideimport.DefaultSource(list); ok {
				src = &s
			} else {
				rep.Notes = append(rep.Notes, "no configuration of the reference IDE found (searched "+strings.Join(ideimport.DefaultRoots(a.ideDiscover()), ", ")+"; use from)")
			}
		}
		if src != nil {
			rep.Source = src
			plan, err := ideimport.ScanApp(*src, ideimport.AppOptions{GOOS: a.opts.GOOS, Home: a.ideHome()})
			if err != nil {
				return rep, Errorf(CodeFailed, "%w", err)
			}
			plans = append(plans, originPlan{plan, ideimport.ScopeGlobal})
		}
	}
	if root := a.ProjectRoot(); root != "" && !p.NoProject {
		if ideimport.HasProjectConfig(root) {
			rep.Project = root
			plan, err := ideimport.ScanProject(ctx, root, ideimport.ProjectOptions{Home: a.ideHome(), App: src, ComposeEnv: a.opts.ComposeEnv})
			if err != nil {
				return rep, Errorf(CodeFailed, "%w", err)
			}
			plans = append(plans, originPlan{plan, ideimport.ScopeProject})
		} else {
			rep.Notes = append(rep.Notes, "the project has no "+ideimport.IdeaDir+" directory")
		}
	}
	if len(plans) == 0 {
		reasons := slices.Clone(rep.Notes)
		switch {
		case p.NoApp && p.NoProject:
			reasons = append(reasons, "both the application configuration and the project were excluded")
		case a.ProjectRoot() == "" && !p.NoProject:
			reasons = append(reasons, "no project open")
		}
		return rep, Errorf(CodeNotFound, "nothing to import: %s", strings.Join(reasons, "; "))
	}
	for _, op := range plans {
		items := slices.Clone(op.plan.Items)
		for _, c := range op.plan.Components {
			for _, it := range a.ideComponentItems(ctx, c) {
				it.AppDefined = false // only the core scan may say so
				items = append(items, it)
			}
		}
		for i := range items {
			a.ideGuardItem(&items[i], op.origin)
		}
		rep.Items = append(rep.Items, items...)
	}
	unselected := make([]bool, len(rep.Items))
	for i := range rep.Items {
		it := &rep.Items[i]
		if it.Status == "" {
			it.Status = ideimport.StatusReady
		}
		if it.Status != ideimport.StatusReady {
			continue
		}
		if !selected[it.Area] {
			unselected[i] = true
			it.Status, it.Detail = ideimport.StatusSkipped, ideimport.JoinDetail(it.Detail, "area not selected")
			continue
		}
		if it.Executable != "" && !p.AllowProjectExecutables {
			it.Status, it.Detail = ideimport.StatusSkipped, ideimport.JoinDetail(it.Detail, ideNeedsOptIn)
			continue
		}
		a.ideCheckItem(ctx, it)
	}
	if !p.DryRun {
		if ideWritesGlobal(rep.Items) {
			path, err := a.ideBackupGlobal()
			if err != nil {
				return rep, Errorf(CodeFailed, "back up the global settings before the import: %w", err)
			}
			rep.Backup = path
			rep.Notes = append(rep.Notes, "previous global settings saved to "+path)
		}
		wrote := false
		for i := range rep.Items {
			if it := &rep.Items[i]; it.Status == ideimport.StatusReady {
				a.ideWriteItem(ctx, it)
				wrote = wrote || it.Status == ideimport.StatusImported
			}
		}
		if wrote {
			a.publishRunConfigsIfProject(ctx)
			sum := rep
			sum.Items = nil
			sum.Counts = countItems(rep.Items)
			a.bus.Publish(TopicIDEImported, sum)
		}
	}
	rep.Counts = countItems(rep.Items)
	for _, ar := range ideimport.Areas {
		info := IDEImportArea{ID: ar.ID, Label: ar.Label, Selected: selected[ar.ID], Default: ar.Default}
		for i, it := range rep.Items {
			if it.Area != ar.ID {
				continue
			}
			info.Total++
			if it.Status == ideimport.StatusReady || it.Status == ideimport.StatusImported || unselected[i] {
				info.Count++
			}
		}
		rep.Areas = append(rep.Areas, info)
	}
	return rep, nil
}

// ideSelectedAreas validates the area selection (empty: defaults).
func ideSelectedAreas(areas []string) (map[string]bool, error) {
	sel := map[string]bool{}
	if len(areas) == 0 {
		for _, a := range ideimport.Areas {
			sel[a.ID] = a.Default
		}
		return sel, nil
	}
	for _, id := range areas {
		id = strings.TrimSpace(id)
		if id == "all" {
			for _, a := range ideimport.Areas {
				sel[a.ID] = true
			}
			continue
		}
		if !slices.ContainsFunc(ideimport.Areas, func(a ideimport.AreaInfo) bool { return a.ID == id }) {
			var ids []string
			for _, a := range ideimport.Areas {
				ids = append(ids, a.ID)
			}
			return nil, Errorf(CodeInvalidParams, "unknown area %q (%s, all)", id, strings.Join(ids, ", "))
		}
		sel[id] = true
	}
	return sel, nil
}

func countItems(items []ideimport.Item) IDEImportCounts {
	var c IDEImportCounts
	for _, it := range items {
		switch it.Status {
		case ideimport.StatusReady:
			c.Ready++
		case ideimport.StatusImported:
			c.Imported++
		case ideimport.StatusSkipped:
			c.Skipped++
		case ideimport.StatusUnsupported:
			c.Unsupported++
		case ideimport.StatusFailed:
			c.Failed++
		}
	}
	return c
}

// ideComponentItems offers c to the plugins. Unhandled components are
// reported as unsupported unless they are quiet.
func (a *App) ideComponentItems(ctx context.Context, c ideimport.Component) []ideimport.Item {
	q, err := HookIDEImportComponent.Apply(ctx, a.hooks, IDEComponentQuery{Component: c})
	if err != nil {
		a.log.Warn("import component", "component", c.Name, "err", err)
	}
	if q.Handled {
		return q.Items
	}
	if c.Kind == ideimport.KindExternalTool {
		// The core run type external-tool takes the tools (D443).
		if a.ProjectRoot() == "" {
			et := ideimport.ParseExternalTool(c.Element)
			return []ideimport.Item{{Area: ideimport.AreaTools, Name: "External tool " + et.Name, Source: c.File, Status: ideimport.StatusSkipped,
				Detail: "external tools are run configurations of a project: open the project and import again"}}
		}
		return []ideimport.Item{ideimport.ExternalToolItem(c)}
	}
	if c.Quiet {
		return nil
	}
	it := ideimport.Item{Area: ideimport.AreaPlugins, Name: c.Name, Source: c.File, Status: ideimport.StatusUnsupported,
		Detail: "no enabled plugin takes this component"}
	switch c.Kind {
	case ideimport.KindRunConfiguration:
		it.Area, it.Name = ideimport.AreaRun, "Run configuration "+c.Element.Attr("name")
		it.Detail = fmt.Sprintf("type %s is not supported (or its plugin is disabled)", c.Name)
	}
	return []ideimport.Item{it}
}

// ideNeedsOptIn is the detail of an item held back without
// AllowProjectExecutables.
const ideNeedsOptIn = "not imported without the opt-in for programs from the repository (allowProjectExecutables)"

// ideGuardItem enforces the trust rules of an item from origin (the
// configuration it was read from; D440), whoever mapped it:
//   - the project's .idea may only write project settings and run
//     configurations; other changes are dropped and named in the detail
//     (all dropped: the item fails);
//   - a change that makes Tiegel run a program named by the project, or
//     lying inside the project, marks the item Executable: it is imported
//     only with AllowProjectExecutables. An interpreter the project only
//     selects from the user's application configuration (AppDefined) is
//     trusted like that configuration.
func (a *App) ideGuardItem(it *ideimport.Item, origin string) {
	if origin != ideimport.ScopeGlobal {
		var keep []ideimport.Change
		var dropped []string
		for _, c := range it.Changes {
			if c.RunConfig == nil && c.Scope != ideimport.ScopeProject {
				dropped = append(dropped, c.Key)
				continue
			}
			keep = append(keep, c)
		}
		if len(dropped) > 0 {
			it.Changes = keep
			it.Detail = ideimport.JoinDetail(it.Detail, "ignored "+strings.Join(dropped, ", ")+": the project configuration cannot change global settings")
			if len(keep) == 0 && (it.Status == ideimport.StatusReady || it.Status == "") {
				it.Status = ideimport.StatusFailed
			}
		}
	}
	root := a.ProjectRoot()
	// A program the user defined in the application configuration (the
	// interpreter a project only names) is trusted like that
	// configuration.
	trusted := origin == ideimport.ScopeGlobal || it.AppDefined
	for _, c := range it.Changes {
		prog := a.ideProgram(c)
		if prog == "" || (trusted && !ideInsideProject(prog, root)) {
			continue
		}
		it.Executable = prog
		break
	}
	if it.Executable != "" {
		it.Detail = ideimport.JoinDetail(it.Detail, "executes a program from the repository: "+it.Executable)
	}
}

// ideProgram returns the program a change makes Tiegel run ("" if none):
// settings declared Executable (or marked by the plugin), a local
// interpreter, and the program of external tool and command run
// configurations.
func (a *App) ideProgram(c ideimport.Change) string {
	if rc := c.RunConfig; rc != nil {
		switch rc.Type {
		case run.TypeExternalTool:
			return strings.TrimSpace(rc.String(run.ToolFieldProgram, ""))
		case run.TypeCommand:
			if words, err := run.SplitWords(rc.String("command", "")); err == nil && len(words) > 0 {
				return words[0]
			}
			return strings.TrimSpace(rc.String("command", ""))
		}
		return ""
	}
	value := ""
	switch v := c.Value.(type) {
	case string:
		value = strings.TrimSpace(v)
	case nil:
	default:
		value = fmt.Sprint(v)
	}
	if value == "" {
		return ""
	}
	if c.Key == ideimport.KeyInterpreter {
		p, _ := strings.CutPrefix(value, "local:")
		if p == value {
			return "" // container interpreters run inside their container
		}
		return p
	}
	if c.Executable {
		return value
	}
	if spec, _, ok := a.lookupSpec(c.Key); ok && spec.Executable {
		return value
	}
	return ""
}

// ideInsideProject reports whether program lies inside the project root:
// absolute below it, "$ProjectFileDir$/…", or relative with a separator
// (Tiegel resolves those against the project).
func ideInsideProject(program, root string) bool {
	if root == "" {
		return false
	}
	p := strings.ReplaceAll(program, "$ProjectFileDir$", root)
	if !filepath.IsAbs(p) {
		if !strings.ContainsAny(p, `/\`) {
			return false // a name from PATH
		}
		p = filepath.Join(root, filepath.FromSlash(p))
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ideWritesGlobal reports whether a ready item changes a global setting.
func ideWritesGlobal(items []ideimport.Item) bool {
	for _, it := range items {
		if it.Status != ideimport.StatusReady {
			continue
		}
		for _, c := range it.Changes {
			if c.RunConfig == nil && (c.Scope == ideimport.ScopeGlobal || c.Scope == "") {
				return true
			}
		}
	}
	return false
}

// ideBackupDir is the directory of the settings backups below the
// global settings directory.
const ideBackupDir = "import-backups"

// ideBackupGlobal writes all global settings as they are now to
// <config>/import-backups/settings-<time>.json (the format of the
// settings file, so a copy restores them) and returns the path (D441).
func (a *App) ideBackupGlobal() (string, error) {
	st := a.global
	values := map[string]json.RawMessage{}
	for _, k := range st.Keys() {
		if v, ok := st.Raw(k); ok {
			values[k] = v
		}
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	dir := filepath.Join(st.Dir(), ideBackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup directory: %w", err)
	}
	base := "settings-" + time.Now().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		name := base + ".json"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.json", base, i+1)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backup: %w", err)
		}
		_, werr := f.Write(append(data, '\n'))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("backup: %w", werr)
		}
		return path, nil
	}
	return "", errors.New("backup: no free file name")
}

// ideStore returns the settings store of a change scope.
func (a *App) ideStore(scope string) (*settings.Store, error) {
	switch scope {
	case ideimport.ScopeGlobal, "":
		return a.global, nil
	case ideimport.ScopeProject:
		if ps := a.ProjectSettings(); ps != nil {
			return ps, nil
		}
		return nil, errors.New("needs an open project")
	}
	return nil, fmt.Errorf("unknown scope %q", scope)
}

// ideCheckItem validates the changes of a ready item and marks it
// skipped when nothing would change, failed when a change is invalid.
func (a *App) ideCheckItem(ctx context.Context, it *ideimport.Item) {
	if len(it.Changes) == 0 {
		it.Status = ideimport.StatusSkipped
		return
	}
	changes := 0
	var notes []string
	for _, c := range it.Changes {
		same, note, err := a.ideCheckChange(ctx, c)
		if err != nil {
			it.Status, it.Detail = ideimport.StatusFailed, ideimport.JoinDetail(it.Detail, err.Error())
			return
		}
		if !same {
			changes++
			if note != "" {
				notes = append(notes, note)
			}
		}
	}
	for _, n := range notes {
		it.Detail = ideimport.JoinDetail(it.Detail, n)
	}
	if changes == 0 {
		it.Status, it.Detail = ideimport.StatusSkipped, ideimport.JoinDetail(it.Detail, "already set")
	}
}

// ideCheckChange validates c and reports whether it would change nothing
// and, for a run configuration, what happens to an existing one.
func (a *App) ideCheckChange(ctx context.Context, c ideimport.Change) (same bool, note string, err error) {
	if c.RunConfig != nil {
		if a.ProjectSettings() == nil {
			return false, "", errors.New("run configurations need an open project")
		}
		t, ok := a.configType(ctx, c.RunConfig.Type)
		if !ok {
			return false, "", fmt.Errorf("run configuration type %q is not available", c.RunConfig.Type)
		}
		checked, err := t.Check(jsonRoundTrip(*c.RunConfig))
		if err != nil {
			return false, "", err
		}
		list, _ := a.storedRunConfigs()
		target, old, note := ideRunTarget(checked, list)
		if old != nil {
			return jsonEqual(*old, target), note, nil
		}
		return false, note, nil
	}
	st, err := a.ideStore(c.Scope)
	if err != nil {
		return false, "", fmt.Errorf("%s: %w", c.Key, err)
	}
	v, err := a.ideMergedValue(st, c)
	if err != nil {
		return false, "", err
	}
	if spec, _, declared := a.lookupSpec(c.Key); declared {
		if c.Scope == ideimport.ScopeProject && spec.Scope == settings.ScopeGlobal {
			return false, "", fmt.Errorf("%s is a global setting", c.Key)
		}
		if err := spec.Check(v); err != nil {
			return false, "", fmt.Errorf("%s: %w", c.Key, err)
		}
	} else if !ideUndeclaredOK(c.Key) {
		return false, "", fmt.Errorf("%s is not a declared setting", c.Key)
	}
	cur, ok, _ := settings.Lookup[json.RawMessage](st, c.Key)
	return ok && jsonEqual(json.RawMessage(cur), v), "", nil
}

// ideRunTarget decides where an imported run configuration goes (D442):
// an existing configuration of the same name (case-insensitive) and the
// same type is replaced and keeps its ID; one of another type is never
// replaced, the import takes the first free name "<name> (imported)",
// "<name> (imported 2)", … (or replaces an earlier import of the same
// type there). It returns the configuration to store, the one it
// replaces (nil: new) and a note for the report.
func ideRunTarget(cfg run.Config, list []run.Config) (run.Config, *run.Config, string) {
	name := cfg.Name
	var other []string
	for i := 0; i < 100; i++ {
		cand := name
		switch {
		case i == 1:
			cand = name + " (imported)"
		case i > 1:
			cand = fmt.Sprintf("%s (imported %d)", name, i)
		}
		idx := slices.IndexFunc(list, func(o run.Config) bool { return strings.EqualFold(o.Name, cand) })
		if idx >= 0 && list[idx].Type != cfg.Type {
			if i == 0 {
				other = append(other, list[idx].Type)
			}
			continue
		}
		cfg.Name = cand
		note := ""
		if i > 0 {
			note = fmt.Sprintf("a configuration %q of type %s exists and is kept; imported as %q", name, strings.Join(other, ", "), cand)
		}
		if idx < 0 {
			cfg.ID = ""
			return cfg, nil, note
		}
		old := list[idx]
		cfg.ID = old.ID
		if note == "" {
			note = fmt.Sprintf("replaces the existing configuration %q (%s)", old.Name, old.Type)
		}
		return cfg, &old, note
	}
	return cfg, nil, ""
}

// ideUndeclaredOK reports the undeclared keys an import may write: the
// client namespaces and the interpreter keys of the kernel contract (the
// path mappings are an object list without spec; the others are declared
// by php-runtime when it is installed).
func ideUndeclaredOK(key string) bool {
	switch key {
	case ideimport.KeyMappings, ideimport.KeyInterpreter, ideimport.KeyPHPVersionOverride, ideimport.KeyExecMode, ideimport.KeyDockerImages:
		return true
	}
	for _, pre := range clientSettingPrefixes {
		if strings.HasPrefix(key, pre) && len(key) > len(pre) {
			return true
		}
	}
	return false
}

// ideMergedValue returns the value c writes (merged with the stored
// value for list and keymap merges), decoded from JSON like a value of
// settings.set.
func (a *App) ideMergedValue(st *settings.Store, c ideimport.Change) (any, error) {
	switch c.Merge {
	case ideimport.MergeList:
		add, ok := c.Value.([]string)
		if !ok {
			return nil, fmt.Errorf("%s: list merge needs a string list", c.Key)
		}
		cur, _, _ := settings.Lookup[[]string](st, c.Key)
		out := slices.Clone(cur)
		for _, s := range add {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		return jsonRoundTrip[any](out), nil
	case ideimport.MergeKeymap:
		lines, ok := c.Value.([]string)
		if !ok {
			return nil, fmt.Errorf("%s: keymap merge needs override lines", c.Key)
		}
		imp, err := keymap.ParseOverrides(lines)
		if err != nil {
			return nil, err
		}
		state, _ := keymap.LoadState(st, a.opts.GOOS)
		for act, chords := range imp {
			state.Drop(act)
			state.Overrides[act] = chords
		}
		return jsonRoundTrip[any](state.Lines()), nil
	}
	return jsonRoundTrip[any](c.Value), nil
}

// ideWriteItem writes the changes of a checked item.
func (a *App) ideWriteItem(ctx context.Context, it *ideimport.Item) {
	var events []string
	for _, c := range it.Changes {
		if err := a.ideWriteChange(ctx, c); err != nil {
			it.Status, it.Detail = ideimport.StatusFailed, ideimport.JoinDetail(it.Detail, err.Error())
			return
		}
		if c.Event != "" {
			events = append(events, c.Event)
		}
	}
	it.Status = ideimport.StatusImported
	for _, ev := range events {
		a.ideEvent(ev)
	}
}

func (a *App) ideWriteChange(ctx context.Context, c ideimport.Change) error {
	if c.RunConfig != nil {
		t, ok := a.configType(ctx, c.RunConfig.Type)
		if !ok {
			return fmt.Errorf("run configuration type %q is not available", c.RunConfig.Type)
		}
		checked, err := t.Check(jsonRoundTrip(*c.RunConfig))
		if err != nil {
			return err
		}
		list, err := a.storedRunConfigs()
		if err != nil {
			return err
		}
		checked, _, _ = ideRunTarget(checked, list)
		_, err = a.storeRunConfig(checked)
		return err
	}
	st, err := a.ideStore(c.Scope)
	if err != nil {
		return err
	}
	if c.Merge == ideimport.MergeKeymap || c.Key == keymap.SettingPreset {
		keymapMu.Lock()
		defer keymapMu.Unlock()
	}
	v, err := a.ideMergedValue(st, c)
	if err != nil {
		return err
	}
	if err := settings.Set(st, c.Key, v); err != nil {
		return fmt.Errorf("%s: %w", c.Key, err)
	}
	a.settingApplied(c.Key)
	return nil
}

// ideEvent publishes the event of a written change.
func (a *App) ideEvent(ev string) {
	switch ev {
	case ideimport.EventInterpreter:
		id := SelectedInterpreter(a.settingsStore())
		a.bus.Publish(TopicInterpreterChanged, InterpreterChangedEvent{ID: id})
	case ideimport.EventMappings:
		ms, _, _ := settings.Lookup[[]MappingInfo](a.settingsStore(), ideimport.KeyMappings)
		a.bus.Publish(TopicMappingsChanged, MappingsChangedEvent{Mappings: ms, Source: "settings"})
	case ideimport.EventPHPVersion:
		a.bus.Publish(TopicPHPVersionChanged, PHPVersionChangedEvent{Override: settings.Get(a.settingsStore(), SettingPHPVersionOverride, "")})
	}
}

func (a *App) publishRunConfigsIfProject(ctx context.Context) {
	if a.ProjectRoot() != "" {
		a.publishRunConfigs(ctx)
	}
}

// jsonRoundTrip returns v encoded and decoded as T (values then have the
// types a JSON client would send: numbers float64, lists []any).
func jsonRoundTrip[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	var u, w any
	if json.Unmarshal(x, &u) != nil || json.Unmarshal(y, &w) != nil {
		return bytes.Equal(x, y)
	}
	x, _ = json.Marshal(u)
	y, _ = json.Marshal(w)
	return bytes.Equal(x, y)
}
