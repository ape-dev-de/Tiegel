package api

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Fixtures: the synthetic configurations of internal/ideimport/testdata.
// newTestApp points IDEImport.Home at a temporary directory, so no test
// reads the real configuration of the user.

func fixtureConfig(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "ideimport", "testdata", "config", "RefIDE2025.2"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyTree copies the fixture project to a temporary directory.
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func reportItem(t *testing.T, rep IDEImportReport, name string) ideimport.Item {
	t.Helper()
	for _, it := range rep.Items {
		if it.Name == name {
			return it
		}
	}
	var names []string
	for _, it := range rep.Items {
		names = append(names, it.Name)
	}
	t.Fatalf("no item %q in %q", name, names)
	return ideimport.Item{}
}

func TestIDEImportMergeErrors(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []ideimport.Change{
		{Key: "ui.x", Scope: ideimport.ScopeGlobal, Value: "x", Merge: ideimport.MergeList},
		{Key: keymap.SettingOverrides, Scope: ideimport.ScopeGlobal, Value: 1, Merge: ideimport.MergeKeymap},
		{Key: keymap.SettingOverrides, Scope: ideimport.ScopeGlobal, Value: []string{"edit.undo Nope+Nope"}, Merge: ideimport.MergeKeymap},
		{Key: "ui.x", Scope: ideimport.ScopeProject, Value: 1},
		{Key: "ui.x", Scope: "elsewhere", Value: 1},
		{RunConfig: &run.Config{Name: "x", Type: run.TypeCommand}},
	} {
		if _, err := a.ideCheckChange(context.Background(), c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	err := tryCall(a, "import.ide.scan", IDEImportParams{NoApp: true, NoProject: true}, nil)
	if ErrorCode(err) != CodeNotFound || !strings.Contains(err.Error(), "excluded") {
		t.Errorf("both excluded: %v", err)
	}
}

func TestIDEImportKeysMatchKernel(t *testing.T) {
	if ideimport.KeyInterpreter != SettingInterpreter || ideimport.KeyPHPVersionOverride != SettingPHPVersionOverride {
		t.Error("ideimport keys differ from the kernel's")
	}
}

func TestIDEImportSources(t *testing.T) {
	a := newTestApp(t)
	var list []ideimport.Source
	call(t, a, "import.ide.sources", nil, &list)
	if len(list) != 0 {
		t.Fatalf("sources in an empty home: %+v", list)
	}
	root := filepath.Dir(fixtureConfig(t))
	a.opts.IDEImport.Roots = []string{root}
	call(t, a, "import.ide.sources", nil, &list)
	if len(list) != 3 || list[0].Name != "RefIDE2025.2" {
		t.Errorf("sources = %+v", list)
	}
	call(t, a, "import.ide.sources", IDEImportSourcesParams{Dir: fixtureConfig(t)}, &list)
	if len(list) != 1 || !list[0].PHP {
		t.Errorf("dir = %+v", list)
	}
	wantCode(t, a, "import.ide.sources", IDEImportSourcesParams{Dir: t.TempDir()}, CodeInvalidParams)
	// The default root below the (temporary) home is searched.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", ideimport.VendorDir, "RefIDE2030.1", "options"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.opts.IDEImport = IDEImportOptions{Home: home}
	call(t, a, "import.ide.sources", nil, &list)
	if len(list) != 1 || list[0].Version != "2030.1" {
		t.Errorf("default root = %+v", list)
	}
}

func TestIDEImportNothing(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "import.ide.scan", nil, CodeNotFound)
	wantCode(t, a, "import.ide.scan", IDEImportParams{From: t.TempDir()}, CodeInvalidParams)
	wantCode(t, a, "import.ide.scan", IDEImportParams{From: fixtureConfig(t), Areas: []string{"nope"}}, CodeInvalidParams)
}

// globalKeys are core settings the fixture changes.
var globalKeys = []string{settings.KeyTheme, settings.KeyEditorFontFamily, settings.KeyEditorFontSize, keymap.SettingOverrides, keymap.SettingPreset, "ui.recentProjects"}

func TestIDEImportDryRunWritesNothing(t *testing.T) {
	a := newTestApp(t)
	before, _ := os.ReadDir(a.ConfigDir())
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t), DryRun: true, Areas: []string{"all"}}, &rep)
	if !rep.DryRun || rep.Counts.Ready == 0 || rep.Counts.Imported != 0 {
		t.Fatalf("dry run counts = %+v", rep.Counts)
	}
	for _, k := range globalKeys {
		if _, ok := a.Settings().Raw(k); ok {
			t.Errorf("dry run wrote %s", k)
		}
	}
	after, _ := os.ReadDir(a.ConfigDir())
	if len(before) != len(after) {
		t.Errorf("dry run created files: %v → %v", before, after)
	}
	var scan IDEImportReport
	call(t, a, "import.ide.scan", IDEImportParams{From: fixtureConfig(t), Areas: []string{"all"}}, &scan)
	if !scan.DryRun || scan.Counts != rep.Counts {
		t.Errorf("scan = %+v, apply dry run = %+v", scan.Counts, rep.Counts)
	}
	if rep.Source == nil || rep.Source.Name != "RefIDE2025.2" {
		t.Errorf("source = %+v", rep.Source)
	}
}

func TestIDEImportApp(t *testing.T) {
	a := newTestApp(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "code", "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.opts.IDEImport.Home = home
	// An existing user binding of another action survives the import.
	if err := settings.Set(a.Settings(), keymap.SettingOverrides, []string{"edit.joinLines Cmd+J"}); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(a.Settings(), "ui.recentProjects", []string{"/kept"}); err != nil {
		t.Fatal(err)
	}
	var events []IDEImportReport
	done := make(chan struct{}, 1)
	a.Bus().Subscribe(TopicIDEImported, func(e bus.Event) {
		if r, ok := e.Payload.(IDEImportReport); ok {
			events = append(events, r)
			done <- struct{}{}
		}
	})
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t)}, &rep)
	if rep.Counts.Imported == 0 || rep.Counts.Failed != 0 || rep.Counts.Ready != 0 {
		t.Fatalf("counts = %+v (%+v)", rep.Counts, rep.Items)
	}
	<-done
	if len(events) != 1 || events[0].Items != nil {
		t.Errorf("event = %+v", events)
	}
	if v := settings.Get(a.Settings(), settings.KeyTheme, ""); v != "dark" {
		t.Errorf("theme = %q", v)
	}
	if v := settings.Get(a.Settings(), settings.KeyEditorFontFamily, ""); v != "Fira Code" {
		t.Errorf("font = %q", v)
	}
	if v := settings.Get(a.Settings(), settings.KeyEditorFontSize, 0.0); v != 14.5 {
		t.Errorf("font size = %v", v)
	}
	if v := settings.Get(a.Settings(), settings.KeyRightMargin, 0); v != 140 {
		t.Errorf("right margin = %v", v)
	}
	lines := settings.Get(a.Settings(), keymap.SettingOverrides, []string(nil))
	if !slices.Contains(lines, "edit.joinLines Cmd+J") || !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "code.reformat ") }) {
		t.Errorf("overrides = %q", lines)
	}
	// Recent projects are opt-in; plugin components without plugins are
	// quiet (application options) or unsupported (external tools).
	if it := reportItem(t, rep, "Recent projects"); it.Status != ideimport.StatusSkipped || !strings.Contains(it.Detail, "area not selected") {
		t.Errorf("recent = %+v", it)
	}
	if v := settings.Get(a.Settings(), "ui.recentProjects", []string(nil)); !reflect.DeepEqual(v, []string{"/kept"}) {
		t.Errorf("recent changed: %v", v)
	}
	if it := reportItem(t, rep, "External tool Make Docs"); it.Status != ideimport.StatusUnsupported || !strings.Contains(it.Detail, "make docs") {
		t.Errorf("external tool = %+v", it)
	}
	for _, it := range rep.Items {
		if it.Name == "PhpDebugGeneral" || it.Name == "Registry" {
			t.Errorf("quiet component reported: %+v", it)
		}
	}
	var area IDEImportArea
	for _, ar := range rep.Areas {
		if ar.ID == ideimport.AreaRecent {
			area = ar
		}
	}
	if area.Selected || area.Count != 1 || area.Total != 1 {
		t.Errorf("recent area = %+v", area)
	}

	// Again: everything is already set.
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t)}, &rep)
	if rep.Counts.Imported != 0 {
		for _, it := range rep.Items {
			if it.Status == ideimport.StatusImported {
				t.Errorf("imported again: %+v", it)
			}
		}
	}
	if it := reportItem(t, rep, "Theme"); it.Status != ideimport.StatusSkipped || !strings.HasSuffix(it.Detail, "already set") {
		t.Errorf("theme again = %+v", it)
	}
}

func TestIDEImportRecentProjects(t *testing.T) {
	home := t.TempDir()
	shop := filepath.Join(home, "code", "shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	a.opts.IDEImport.Home = home
	if err := settings.Set(a.Settings(), "ui.recentProjects", []string{"/kept"}); err != nil {
		t.Fatal(err)
	}
	var rep IDEImportReport
	call(t, a, "import.ide.apply", IDEImportParams{From: fixtureConfig(t), Areas: []string{ideimport.AreaRecent}}, &rep)
	if v := settings.Get(a.Settings(), "ui.recentProjects", []string(nil)); !reflect.DeepEqual(v, []string{"/kept", shop}) {
		t.Errorf("recent = %v", v)
	}
	if it := reportItem(t, rep, "Theme"); it.Status != ideimport.StatusSkipped {
		t.Errorf("theme not skipped: %+v", it)
	}
}

// fakePlugin answers the hook like a plugin would (OnIDEComponent needs
// a module context; the hook is the same).
func fakePlugin(a *App) error {
	HookIDEImportComponent.On(a.Hooks(), 0, func(_ context.Context, q IDEComponentQuery) (IDEComponentQuery, error) {
		c := q.Component
		switch {
		case c.Name == "PhpUnit":
			q.Items = append(slices.Clone(q.Items), c.Item("Fake setting", "", c.Setting("ui.fakeImported", true)))
		case c.Name == "LaravelPint":
			q.Items = append(slices.Clone(q.Items), c.Item("Invalid", "", c.Setting(settings.KeyEditorFontSize, "huge")))
		case c.Name == "PhpStan":
			q.Items = append(slices.Clone(q.Items), c.Item("Undeclared", "", c.Setting("fake.key", 1)))
		case c.Kind == ideimport.KindRunConfiguration && c.Element.Attr("name") == "Unit":
			it := ideimport.RunConfigItem(c, run.Config{Name: "Unit", Type: "missing-type"}, "")
			q.Items = append(slices.Clone(q.Items), it)
		case c.Name == "SomeThirdPartyComponent":
			// handled without a report entry
		default:
			return q, nil
		}
		q.Handled = true
		return q, nil
	}, hooks.WithOwner("fake"))
	return nil
}

func TestIDEImportProject(t *testing.T) {
	a := newTestApp(t, fakePlugin)
	dir := copyFixture(t, filepath.Join("..", "ideimport", "testdata", "project"))
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	var st IDEImportStatus
	call(t, a, "import.ide.status", nil, &st)
	if !st.ProjectConfig {
		t.Errorf("status = %+v", st)
	}
	var rep IDEImportReport
	call(t, a, "import.ide.scan", IDEImportParams{NoApp: true}, &rep)
	if rep.Project != a.ProjectRoot() || rep.Source != nil {
		t.Fatalf("report = %+v", rep)
	}
	if it := reportItem(t, rep, "Invalid"); it.Status != ideimport.StatusFailed {
		t.Errorf("invalid value = %+v", it)
	}
	if it := reportItem(t, rep, "Undeclared"); it.Status != ideimport.StatusFailed {
		t.Errorf("undeclared key = %+v", it)
	}
	if it := reportItem(t, rep, "Run configuration Unit"); it.Status != ideimport.StatusFailed {
		t.Errorf("missing type = %+v", it)
	}
	if it := reportItem(t, rep, "Run configuration FooTest::testBar"); it.Status != ideimport.StatusUnsupported || !strings.Contains(it.Detail, "type") {
		t.Errorf("unhandled run type = %+v", it)
	}
	if it := reportItem(t, rep, "PhpIncludePathManager"); it.Status != ideimport.StatusUnsupported {
		t.Errorf("unhandled component = %+v", it)
	}
	for _, it := range rep.Items {
		if it.Name == "SomeThirdPartyComponent" || it.Name == "Git.Settings" {
			t.Errorf("reported: %+v", it)
		}
	}
	ps := a.ProjectSettings()
	if _, ok := ps.Raw(SettingInterpreter); ok {
		t.Fatal("scan wrote the interpreter")
	}

	var mu sync.Mutex
	var events []string
	a.Bus().Subscribe("*", func(e bus.Event) {
		switch e.Topic {
		case TopicInterpreterChanged, TopicMappingsChanged, TopicPHPVersionChanged:
			mu.Lock()
			events = append(events, e.Topic)
			mu.Unlock()
		}
	})
	call(t, a, "import.ide.apply", IDEImportParams{NoApp: true}, &rep)
	if v := settings.Get(ps, SettingInterpreter, ""); v != "compose:fixture:app" {
		t.Errorf("interpreter = %q", v)
	}
	if v := settings.Get(ps, "php-runtime.execMode", ""); v != "run" {
		t.Errorf("exec mode = %q", v)
	}
	if v := settings.Get(ps, SettingPHPVersionOverride, ""); v != "8.2" {
		t.Errorf("version = %q", v)
	}
	maps := settings.Get(ps, ideimport.KeyMappings, []MappingInfo(nil))
	if len(maps) != 3 || maps[2] != (MappingInfo{Host: a.ProjectRoot(), Remote: "/var/www/html"}) {
		t.Errorf("mappings = %+v", maps)
	}
	if v := settings.Get(ps, settings.KeyRightMargin, 0); v != 160 {
		t.Errorf("project right margin = %d", v)
	}
	if v := settings.Get(ps, "ui.fakeImported", false); !v {
		t.Error("plugin item not written")
	}
	var cfgs []RunConfigInfo
	call(t, a, "run.configs.list", nil, &cfgs)
	if len(cfgs) != 1 || cfgs[0].Name != "Cache clear" || cfgs[0].Type != run.TypePHPScript || !cfgs[0].Available {
		t.Fatalf("run configs = %+v", cfgs)
	}
	id := cfgs[0].ID
	// Importing again keeps the configuration (same ID, unchanged).
	call(t, a, "import.ide.apply", IDEImportParams{NoApp: true}, &rep)
	if it := reportItem(t, rep, "Run configuration Cache clear"); it.Status != ideimport.StatusSkipped {
		t.Errorf("again = %+v", it)
	}
	// A changed configuration of the same name is replaced in place.
	cfg := cfgs[0].Config
	cfg.Values = map[string]any{"file": "other.php"}
	call(t, a, "run.configs.save", cfg, nil)
	call(t, a, "import.ide.apply", IDEImportParams{NoApp: true}, &rep)
	call(t, a, "run.configs.list", nil, &cfgs)
	if len(cfgs) != 1 || cfgs[0].ID != id || cfgs[0].Values["file"] != "bin/console" {
		t.Errorf("replaced = %+v", cfgs)
	}
	for _, want := range []string{TopicInterpreterChanged, TopicMappingsChanged, TopicPHPVersionChanged} {
		if !eventually(func() bool { mu.Lock(); defer mu.Unlock(); return slices.Contains(events, want) }) {
			t.Errorf("no %s event", want)
		}
	}
	// NoProject: the project is left alone.
	wantCode(t, a, "import.ide.scan", IDEImportParams{NoApp: true, NoProject: true}, CodeNotFound)
}

func TestIDEImportProjectWithoutIdea(t *testing.T) {
	a := newTestApp(t)
	var st IDEImportStatus
	call(t, a, "import.ide.status", nil, &st)
	if st.ProjectConfig || st.Project != "" {
		t.Errorf("status without project = %+v", st)
	}
	if _, err := a.OpenProject(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	call(t, a, "import.ide.status", nil, &st)
	if st.ProjectConfig || st.Project == "" {
		t.Errorf("status without .idea = %+v", st)
	}
	var rep IDEImportReport
	call(t, a, "import.ide.scan", IDEImportParams{From: fixtureConfig(t)}, &rep)
	if rep.Project != "" || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], ".idea") {
		t.Errorf("notes = %+v", rep.Notes)
	}
	// Project settings of an application import need a project: global
	// scope here, so it works.
	if rep.Counts.Failed != 0 {
		t.Errorf("failed = %+v", rep.Counts)
	}
}

// eventually polls cond for up to two seconds (bus delivery is async).
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
