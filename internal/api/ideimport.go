package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
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
	// .idea directory).
	Notes []string `json:"notes,omitempty"`
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
	var plans []ideimport.Plan
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
			plans = append(plans, plan)
		}
	}
	if root := a.ProjectRoot(); root != "" && !p.NoProject {
		if ideimport.HasProjectConfig(root) {
			rep.Project = root
			plan, err := ideimport.ScanProject(ctx, root, ideimport.ProjectOptions{Home: a.ideHome(), App: src, ComposeEnv: a.opts.ComposeEnv})
			if err != nil {
				return rep, Errorf(CodeFailed, "%w", err)
			}
			plans = append(plans, plan)
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
	for _, plan := range plans {
		rep.Items = append(rep.Items, plan.Items...)
		for _, c := range plan.Components {
			rep.Items = append(rep.Items, a.ideComponentItems(ctx, c)...)
		}
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
		a.ideCheckItem(ctx, it)
	}
	if !p.DryRun {
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
	if c.Quiet {
		return nil
	}
	it := ideimport.Item{Area: ideimport.AreaPlugins, Name: c.Name, Source: c.File, Status: ideimport.StatusUnsupported,
		Detail: "no enabled plugin takes this component"}
	switch c.Kind {
	case ideimport.KindRunConfiguration:
		it.Area, it.Name = ideimport.AreaRun, "Run configuration "+c.Element.Attr("name")
		it.Detail = fmt.Sprintf("type %s is not supported (or its plugin is disabled)", c.Name)
	case ideimport.KindExternalTool:
		et := ideimport.ParseExternalTool(c.Element)
		it.Area, it.Name = ideimport.AreaTools, "External tool "+et.Name
		it.Detail = strings.TrimSpace("no run configuration type for external tools: " + et.Program + " " + et.Args)
	}
	return []ideimport.Item{it}
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
	for _, c := range it.Changes {
		same, err := a.ideCheckChange(ctx, c)
		if err != nil {
			it.Status, it.Detail = ideimport.StatusFailed, ideimport.JoinDetail(it.Detail, err.Error())
			return
		}
		if !same {
			changes++
		}
	}
	if changes == 0 {
		it.Status, it.Detail = ideimport.StatusSkipped, ideimport.JoinDetail(it.Detail, "already set")
	}
}

// ideCheckChange validates c and reports whether it would change nothing.
func (a *App) ideCheckChange(ctx context.Context, c ideimport.Change) (bool, error) {
	if c.RunConfig != nil {
		if a.ProjectSettings() == nil {
			return false, errors.New("run configurations need an open project")
		}
		t, ok := a.configType(ctx, c.RunConfig.Type)
		if !ok {
			return false, fmt.Errorf("run configuration type %q is not available", c.RunConfig.Type)
		}
		checked, err := t.Check(jsonRoundTrip(*c.RunConfig))
		if err != nil {
			return false, err
		}
		list, _ := a.storedRunConfigs()
		for _, o := range list {
			if strings.EqualFold(o.Name, checked.Name) {
				checked.ID = o.ID
				return jsonEqual(o, checked), nil
			}
		}
		return false, nil
	}
	st, err := a.ideStore(c.Scope)
	if err != nil {
		return false, fmt.Errorf("%s: %w", c.Key, err)
	}
	v, err := a.ideMergedValue(st, c)
	if err != nil {
		return false, err
	}
	if spec, _, declared := a.lookupSpec(c.Key); declared {
		if c.Scope == ideimport.ScopeProject && spec.Scope == settings.ScopeGlobal {
			return false, fmt.Errorf("%s is a global setting", c.Key)
		}
		if err := spec.Check(v); err != nil {
			return false, fmt.Errorf("%s: %w", c.Key, err)
		}
	} else if !ideUndeclaredOK(c.Key) {
		return false, fmt.Errorf("%s is not a declared setting", c.Key)
	}
	cur, ok, _ := settings.Lookup[json.RawMessage](st, c.Key)
	return ok && jsonEqual(json.RawMessage(cur), v), nil
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
		if list, err := a.storedRunConfigs(); err == nil {
			for _, o := range list {
				if strings.EqualFold(o.Name, checked.Name) {
					checked.ID = o.ID
				}
			}
		}
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
