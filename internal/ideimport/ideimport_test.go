package ideimport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// The fixtures under testdata are synthetic; tests never read the real
// configuration directories of the user (Roots/Home point to fixtures).

func fixtureSource(t *testing.T) Source {
	t.Helper()
	src, err := SourceAt(filepath.Join("testdata", "config", "RefIDE2025.2"))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func item(t *testing.T, items []Item, name string) Item {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	t.Fatalf("no item %q in %q", name, names)
	return Item{}
}

func change(t *testing.T, it Item, key string) Change {
	t.Helper()
	for _, c := range it.Changes {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("item %q has no change of %s: %+v", it.Name, key, it.Changes)
	return Change{}
}

func TestFindSources(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "config"))
	list, err := FindSources(DiscoverOptions{Roots: []string{root, filepath.Join(root, "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range list {
		names = append(names, s.Name)
	}
	want := []string{"RefIDE2025.2", "OtherIDE2025.2", "RefIDE2024.3"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("sources = %v, want %v", names, want)
	}
	if list[0].Product != "RefIDE" || list[0].Version != "2025.2" || !list[0].PHP || list[1].PHP {
		t.Errorf("parsed = %+v / %+v", list[0], list[1])
	}
	def, ok := DefaultSource(list)
	if !ok || def.Name != "RefIDE2025.2" {
		t.Errorf("default = %+v", def)
	}
	if def, ok := DefaultSource(list[1:2]); !ok || def.Name != "OtherIDE2025.2" {
		t.Errorf("default without php = %+v", def)
	}
	if _, ok := DefaultSource(nil); ok {
		t.Error("default of nothing")
	}
	if _, err := SourceAt(filepath.Join(root, "notes")); err == nil {
		t.Error("notes accepted as configuration")
	}
}

func TestDefaultRoots(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tc := range []struct {
		o    DiscoverOptions
		want string
	}{
		{DiscoverOptions{GOOS: "darwin", Home: "/h"}, filepath.Join("/h", "Library", "Application Support", VendorDir)},
		{DiscoverOptions{GOOS: "linux", Home: "/h"}, filepath.Join("/h", ".config", VendorDir)},
		{DiscoverOptions{GOOS: "linux", Home: "/h", XDGConfig: "/x"}, filepath.Join("/x", VendorDir)},
		{DiscoverOptions{GOOS: "windows", AppData: "C:" + sep + "AD"}, filepath.Join("C:"+sep+"AD", VendorDir)},
		{DiscoverOptions{GOOS: "windows", Home: "/h"}, filepath.Join("/h", "AppData", "Roaming", VendorDir)},
	} {
		got := DefaultRoots(tc.o)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%+v: %v, want %s", tc.o, got, tc.want)
		}
	}
	if got := DefaultRoots(DiscoverOptions{GOOS: "darwin"}); got != nil {
		t.Errorf("no home: %v", got)
	}
	if compareVersions("2025.10", "2025.2") <= 0 || compareVersions("", "1") >= 0 || compareVersions("1.0", "1") != 0 {
		t.Error("compareVersions")
	}
}

func TestMacros(t *testing.T) {
	m := Macros{ProjectDir: filepath.FromSlash("/p/shop"), UserHome: filepath.FromSlash("/home/u"), ConfigDir: filepath.FromSlash("/cfg")}
	if got := m.Expand("$PROJECT_DIR$/a:$MODULE_DIR$:$USER_HOME$:$APPLICATION_CONFIG_DIR$:$OTHER$"); got != "/p/shop/a:/p/shop:/home/u:/cfg:$OTHER$" {
		t.Errorf("Expand = %q", got)
	}
	if u := m.Unresolved("$PROJECT_DIR$ $X$"); !reflect.DeepEqual(u, []string{"$X$"}) {
		t.Errorf("Unresolved = %v", u)
	}
	if p := m.Path("file://$PROJECT_DIR$/vendor/bin/x"); p != filepath.FromSlash("/p/shop/vendor/bin/x") {
		t.Errorf("Path = %q", p)
	}
	if p := m.Path("$UNKNOWN$/x"); p != "" {
		t.Errorf("unresolved path = %q", p)
	}
	if p := m.Path("  "); p != "" {
		t.Errorf("empty path = %q", p)
	}
	if r := m.ProjectRel(filepath.FromSlash("/p/shop/tests/A.php")); r != "tests/A.php" {
		t.Errorf("ProjectRel = %q", r)
	}
	if r := m.ProjectRel(filepath.FromSlash("/p/other")); r != filepath.FromSlash("/p/other") {
		t.Errorf("ProjectRel outside = %q", r)
	}
	if r := (Macros{}).ProjectRel("x"); r != "x" {
		t.Errorf("ProjectRel without project = %q", r)
	}
}

func TestParseElement(t *testing.T) {
	root, err := Parse(strings.NewReader(`<?xml version="1.0"?><application><component name="A" x="1"><option name="o" value="v"/><option name="t">text</option><option name="m"><map><entry key="k"/></map></option></component><component name="B"/></application>`))
	if err != nil {
		t.Fatal(err)
	}
	a := root.Component("A")
	if a.Attr("x") != "1" || a.Attr("nope") != "" {
		t.Error("Attr")
	}
	if v, ok := a.Option("o"); !ok || v != "v" {
		t.Errorf("Option o = %q %v", v, ok)
	}
	if v, ok := a.Option("t"); !ok || v != "text" {
		t.Errorf("Option t = %q %v", v, ok)
	}
	if _, ok := a.Option("missing"); ok {
		t.Error("missing option found")
	}
	if a.OptionElement("m").Find("entry")[0].Attr("key") != "k" {
		t.Error("OptionElement/Find")
	}
	if root.Component("C") != nil || len(root.Components()) != 2 || len(a.All("option")) != 3 {
		t.Error("Components/All")
	}
	var nilEl *Element
	if nilEl.Child("x") != nil || nilEl.Attr("x") != "" || nilEl.All("x") != nil || nilEl.OptionElement("x") != nil {
		t.Error("nil element helpers")
	}
	if _, ok := nilEl.Option("x"); ok {
		t.Error("nil Option")
	}
	if _, err := Parse(strings.NewReader("")); err == nil {
		t.Error("empty document accepted")
	}
	if _, err := ParseFile(filepath.Join("testdata", "missing.xml")); err == nil {
		t.Error("missing file accepted")
	}
}

func TestScanApp(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"code/shop", "code/older"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := ScanApp(fixtureSource(t), AppOptions{GOOS: "darwin", Home: home})
	if err != nil {
		t.Fatal(err)
	}
	items := plan.Items

	font := item(t, items, "Editor font")
	if change(t, font, settings.KeyEditorFontFamily).Value != "Fira Code" || change(t, font, settings.KeyEditorFontSize).Value != 14.5 ||
		change(t, font, settings.KeyEditorLineHeight).Value != 1.3 || font.Area != AreaEditor {
		t.Errorf("editor font = %+v", font)
	}
	console := item(t, items, "Console font")
	if change(t, console, terminal.KeyFontSize).Value != 12.0 {
		t.Errorf("console font = %+v", console)
	}
	theme := item(t, items, "Theme")
	if change(t, theme, settings.KeyTheme).Value != "dark" {
		t.Errorf("theme = %+v", theme)
	}
	if change(t, item(t, items, "UI font size"), settings.KeyUIFontSize).Value != 14.0 {
		t.Error("ui font size")
	}
	wrap := item(t, items, "Soft wrap")
	if change(t, wrap, settings.KeySoftWrap).Value != settings.SoftWrapFiles ||
		!reflect.DeepEqual(change(t, wrap, settings.KeySoftWrapFiles).Value, []string{"*.md", "*.txt", "*.neon"}) {
		t.Errorf("soft wrap = %+v", wrap)
	}
	if change(t, item(t, items, "Show whitespace"), settings.KeyShowWhitespace).Value != settings.WhitespaceTrailing {
		t.Error("whitespace")
	}
	cs := item(t, items, "Code style Team")
	if change(t, cs, settings.KeyEditorTabWidth).Value != 4 || change(t, cs, settings.KeyEditorInsertSpace).Value != true ||
		change(t, cs, settings.KeyRightMargin).Value != 140 || cs.Source != "codestyles/Team.xml" {
		t.Errorf("code style = %+v", cs)
	}
	shell := item(t, items, "Terminal shell")
	if change(t, shell, terminal.KeyShell).Value != "/bin/sh" || !reflect.DeepEqual(change(t, shell, terminal.KeyShellArgs).Value, []string{"-l"}) {
		t.Errorf("shell = %+v", shell)
	}
	if change(t, item(t, items, "Terminal keys override IDE shortcuts"), terminal.KeyOverrideKeys).Value != false ||
		change(t, item(t, items, "Close terminal tab when the shell exits"), terminal.KeyCloseOnExit).Value != false ||
		change(t, item(t, items, "Terminal: Option as Meta"), terminal.KeyOptionAsMeta).Value != true {
		t.Error("terminal options")
	}
	if change(t, item(t, items, "Open project in"), settings.KeyOpenProjectIn).Value != settings.OpenProjectNew ||
		change(t, item(t, items, "Running processes on close"), run.KeyConfirmClose).Value != run.CloseTerminate {
		t.Error("general")
	}
	recent := item(t, items, "Recent projects")
	rc := change(t, recent, KeyRecentProjects)
	want := []string{filepath.Join(home, "code", "shop"), filepath.Join(home, "code", "older")}
	if !reflect.DeepEqual(rc.Value, want) || rc.Merge != MergeList || recent.Area != AreaRecent || !strings.Contains(recent.Detail, "1 no longer exist") {
		t.Errorf("recent = %+v", recent)
	}
	if it := item(t, items, "PHP interpreters"); it.Status != StatusSkipped {
		t.Errorf("app interpreters = %+v", it)
	}
	if it := item(t, items, "Keymap"); it.Status != StatusReady {
		t.Errorf("keymap = %+v", it)
	}

	// Components: plugin parts (quiet) and the external tool.
	var tool *Component
	names := map[string]Component{}
	for i, c := range plan.Components {
		names[c.Name] = c
		if c.Kind == KindExternalTool {
			tool = &plan.Components[i]
		}
		if coreAppComponents[c.Name] {
			t.Errorf("core component %s handed to plugins", c.Name)
		}
	}
	for _, n := range []string{"PhpDebugGeneral", "Git.Settings", "DockerSettings", "Registry", "PropertiesComponent"} {
		c, ok := names[n]
		if !ok || !c.Quiet || c.Scope != ScopeGlobal || c.Kind != KindComponent {
			t.Errorf("component %s = %+v", n, c)
		}
	}
	if tool == nil || tool.Name != "Make Docs" || tool.Quiet {
		t.Fatalf("external tool = %+v", tool)
	}
	et := ParseExternalTool(tool.Element)
	if et != (ExternalTool{Name: "Make Docs", Description: "Builds the docs", Program: "make", Args: "docs", WorkingDir: "$ProjectFileDir$", Console: true, Sync: true}) {
		t.Errorf("external tool = %+v", et)
	}
}

func TestScanAppVariants(t *testing.T) {
	write := func(t *testing.T, dir, rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	write(t, dir, "options/laf.xml", `<application><component name="LafManager" autodetect="true"/></application>`)
	write(t, dir, "options/terminal-font.xml", `<application><component name="TerminalFontOptions"><option name="FONT_SIZE" value="11"/><option name="FONT_FAMILY" value="Mono"/></component></application>`)
	write(t, dir, "options/console-font.xml", `<application><component name="ConsoleFont"><option name="FONT_SIZE" value="12"/></component></application>`)
	write(t, dir, "options/editor.xml", `<application><component name="EditorSettings"><option name="USE_SOFT_WRAPS" value="MAIN_EDITOR"/><option name="SOFT_WRAP_FILE_MASKS" value="*"/><option name="IS_WHITESPACES_SHOWN" value="false"/></component></application>`)
	write(t, dir, "options/ide.general.xml", `<application><component name="GeneralSettings"><option name="confirmOpenNewProject2" value="7"/><option name="processCloseConfirmation" value="DISCONNECT"/></component></application>`)
	write(t, dir, "options/terminal.xml", `<application><component name="TerminalOptionsProvider"><option name="shellPath" value="/nonexistent/zsh"/></component></application>`)
	write(t, dir, "options/keymap.xml", `<application><component name="KeymapManager"><active_keymap name="Emacs"/></component></application>`)
	write(t, dir, "codestyles/Default.xml", `<code_scheme name="Default"><option name="OTHER_INDENT_OPTIONS"><value><option name="USE_TAB_CHARACTER" value="true"/></value></option></code_scheme>`)
	src, err := SourceAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ScanApp(src, AppOptions{GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	items := plan.Items
	if change(t, item(t, items, "Theme"), settings.KeyTheme).Value != "system" {
		t.Error("autodetect theme")
	}
	if change(t, item(t, items, "Terminal font"), terminal.KeyFontSize).Value != 11.0 {
		t.Error("terminal font")
	}
	if it := item(t, items, "Console font"); it.Status != StatusUnsupported {
		t.Errorf("console font with terminal font = %+v", it)
	}
	if change(t, item(t, items, "Soft wrap"), settings.KeySoftWrap).Value != settings.SoftWrapAll {
		t.Error("soft wrap all")
	}
	if change(t, item(t, items, "Show whitespace"), settings.KeyShowWhitespace).Value != settings.WhitespaceNone {
		t.Error("whitespace none")
	}
	if it := item(t, items, "Open project in"); it.Status != StatusUnsupported {
		t.Errorf("unknown open mode = %+v", it)
	}
	if change(t, item(t, items, "Running processes on close"), run.KeyConfirmClose).Value != run.CloseKeep {
		t.Error("keep")
	}
	if it := item(t, items, "Terminal shell"); it.Status != StatusSkipped {
		t.Errorf("missing shell = %+v", it)
	}
	if it := item(t, items, "Keymap"); it.Status != StatusUnsupported || len(it.Changes) != 0 {
		t.Errorf("emacs keymap = %+v", it)
	}
	cs := item(t, items, "Code style Default")
	if change(t, cs, settings.KeyEditorInsertSpace).Value != false || len(cs.Changes) != 1 {
		t.Errorf("default code style = %+v", cs)
	}
	if themeOf("Light with Light Header") != "light" || themeOf("High contrast") != "dark" || themeOf("Solarized") != "" {
		t.Error("themeOf")
	}
	// An empty configuration has nothing to import.
	empty := t.TempDir()
	write(t, empty, "options/x.txt", "")
	src, _ = SourceAt(empty)
	if plan, err := ScanApp(src, AppOptions{}); err != nil || len(plan.Items) != 0 || len(plan.Components) != 0 {
		t.Errorf("empty = %+v, %v", plan, err)
	}
}

func TestConvertKeystroke(t *testing.T) {
	for in, want := range map[string]string{
		"meta alt L":          "Cmd+Alt+L",
		"shift ctrl F6":       "Ctrl+Shift+F6",
		"meta OPEN_BRACKET":   "Cmd+[",
		"meta CLOSE_BRACKET":  "Cmd+]",
		"meta BACK_SPACE":     "Cmd+Backspace",
		"control shift MINUS": "Ctrl+Shift+-",
		"alt ENTER":           "Alt+Enter",
		"meta NUMPAD1":        "Cmd+1",
		"pressed meta SLASH":  "Cmd+/",
		"altGraph PAGE_DOWN":  "Alt+PageDown",
		"F12":                 "F12",
	} {
		c, err := ConvertKeystroke(in)
		if err != nil || c.String() != want {
			t.Errorf("%q = %v, %v; want %s", in, c, err, want)
		}
	}
	for _, bad := range []string{"", "meta", "meta INSERT", "L meta"} {
		if c, err := ConvertKeystroke(bad); err == nil {
			t.Errorf("%q accepted as %v", bad, c)
		}
	}
}

func TestActionMapTargets(t *testing.T) {
	known := map[keymap.Action]bool{}
	for _, d := range keymap.Defs() {
		known[d.ID] = true
	}
	for ref, a := range ActionMap {
		if !known[a] {
			t.Errorf("%s maps to unknown action %s", ref, a)
		}
	}
}

func TestKeymapImport(t *testing.T) {
	plan, err := ScanApp(fixtureSource(t), AppOptions{GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	km := item(t, plan.Items, "Keymap")
	if change(t, km, keymap.SettingPreset).Value != "mac" || km.Source != "keymaps/My Mac.xml" ||
		!strings.Contains(km.Detail, "My Mac → My Base → Mac OS X 10.5+") {
		t.Errorf("keymap = %+v", km)
	}
	ov := change(t, km, keymap.SettingOverrides)
	if ov.Merge != MergeKeymap {
		t.Errorf("merge = %q", ov.Merge)
	}
	lines, _ := ov.Value.([]string)
	parsed, err := keymap.ParseOverrides(lines)
	if err != nil {
		t.Fatal(err)
	}
	chords := func(a keymap.Action) []string {
		var out []string
		for _, c := range parsed[a] {
			out = append(out, c.String())
		}
		slices.Sort(out)
		return out
	}
	// The child's entry replaces the parent's.
	if got := chords(keymap.DuplicateLines); !reflect.DeepEqual(got, []string{"Cmd+D", "Ctrl+Shift+D"}) {
		t.Errorf("duplicate = %v", got)
	}
	if got := chords("code.reformat"); !reflect.DeepEqual(got, []string{"Cmd+Alt+Shift+L"}) {
		t.Errorf("reformat = %v", got)
	}
	// Removed shortcuts become an unbound override.
	if got, ok := parsed[keymap.DeleteLine]; !ok || len(got) != 0 {
		t.Errorf("delete line = %v %v", got, ok)
	}
	// Only one of two reference actions changed: the defaults stay.
	if got := chords(keymap.MoveLinesUp); !slices.Contains(got, "Cmd+Alt+Shift+Up") || !slices.Contains(got, "Alt+Shift+Up") {
		t.Errorf("move lines up = %v", got)
	}
	// Inherited from the parent; Cmd+` added.
	if got := chords("view.terminal"); !reflect.DeepEqual(got, []string{"Alt+F12", "Cmd+`"}) {
		t.Errorf("terminal = %v", got)
	}
	// Back without Cmd+Alt+Left and the swipe; Go to Class keeps Cmd+O
	// (the second chord is a two-stroke one): no override.
	if got := chords(keymap.NavBack); !reflect.DeepEqual(got, []string{"Cmd+[", "MouseBack"}) {
		t.Errorf("back = %v", got)
	}
	if got := chords("navigate.gotoClass"); got != nil {
		t.Errorf("goto class = %v", got)
	}
	un := item(t, plan.Items, "Shortcuts without counterpart")
	if un.Status != StatusUnsupported || !strings.Contains(un.Detail, "SomePluginAction") {
		t.Errorf("unmapped = %+v", un)
	}
	bad := item(t, plan.Items, "Unsupported shortcuts")
	if !strings.Contains(bad.Detail, "two-stroke") {
		t.Errorf("bad chords = %+v", bad)
	}
	// The pc base on Linux.
	items := mapKeymap("Mine", "options/keymap.xml", map[string]*keymapFile{
		"Mine": {name: "Mine", parent: "$default", rel: "keymaps/Mine.xml", actions: map[string]*Element{}},
	}, "linux")
	if change(t, items[0], keymap.SettingPreset).Value != "pc" {
		t.Errorf("pc keymap = %+v", items)
	}
	// A bundled keymap without file: preset only.
	items = mapKeymap("Mac OS X 10.5+", "options/keymap.xml", nil, "darwin")
	if len(items) != 1 || len(items[0].Changes) != 1 {
		t.Errorf("bundled keymap = %+v", items)
	}
}

func projectFixture(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanProject(t *testing.T) {
	dir := projectFixture(t)
	src := fixtureSource(t)
	plan, err := ScanProject(context.Background(), dir, ProjectOptions{App: &src})
	if err != nil {
		t.Fatal(err)
	}
	items := plan.Items
	in := item(t, items, "PHP interpreter app")
	if change(t, in, KeyInterpreter).Value != "compose:fixture:app" || change(t, in, KeyInterpreter).Event != EventInterpreter ||
		change(t, in, KeyExecMode).Value != "run" || in.Source != ".idea/workspace.xml" {
		t.Errorf("interpreter = %+v", in)
	}
	lvl := item(t, items, "PHP language level")
	if c := change(t, lvl, KeyPHPVersionOverride); c.Value != "8.2" || c.Event != EventPHPVersion {
		t.Errorf("level = %+v", lvl)
	}
	maps := change(t, item(t, items, "Path mappings"), KeyMappings)
	want := []Mapping{
		{Host: filepath.Join(dir, "public"), Remote: "/srv/public"},
		{Host: filepath.Join(dir, "vendor"), Remote: "/var/www/html/vendor"},
		{Host: dir, Remote: "/var/www/html"},
	}
	if !reflect.DeepEqual(maps.Value, want) || maps.Event != EventMappings {
		t.Errorf("mappings = %+v", maps.Value)
	}
	style := item(t, items, "Project code style")
	if len(style.Changes) != 1 || change(t, style, settings.KeyRightMargin).Value != 160 || style.Changes[0].Scope != ScopeProject {
		t.Errorf("project code style = %+v", style)
	}
	script := item(t, items, "Run configuration Cache clear")
	rc := script.Changes[0].RunConfig
	if rc == nil || rc.Type != run.TypePHPScript || script.Area != AreaRun {
		t.Fatalf("php script = %+v", script)
	}
	wantVals := map[string]any{"file": "bin/console", "args": "cache:clear --env=dev", "phpOptions": "-d memory_limit=-1",
		"env": []string{"APP_ENV=dev"}, "interpreter": run.InterpreterProject}
	if !reflect.DeepEqual(rc.Values, wantVals) {
		t.Errorf("php script values = %#v", rc.Values)
	}
	if _, err := coreType(t, run.TypePHPScript).Check(*rc); err != nil {
		t.Errorf("php script does not validate: %v", err)
	}
	if it := item(t, items, "Run configuration All checks"); it.Status != StatusUnsupported || !strings.Contains(it.Detail, "Unit, Cache clear") {
		t.Errorf("compound = %+v", it)
	}
	if it := item(t, items, "ProjectModuleManager"); it.Status != StatusSkipped {
		t.Errorf("module manager = %+v", it)
	}
	// Components for the plugins.
	var runTypes []string
	comps := map[string]Component{}
	for _, c := range plan.Components {
		if c.Kind == KindRunConfiguration {
			runTypes = append(runTypes, c.Name+":"+c.Element.Attr("name"))
			continue
		}
		comps[c.Name] = c
		if _, core := coreProjectComponents[c.Name]; core {
			t.Errorf("core component %s handed to plugins", c.Name)
		}
	}
	slices.Sort(runTypes)
	wantRuns := []string{"PHPUnitRunConfigurationType:BarTest file", "PHPUnitRunConfigurationType:FooTest::testBar",
		"PHPUnitRunConfigurationType:Unit", "PHPUnitRunConfigurationType:Unit directory", "js.build_tools.npm:npm dev"}
	if !reflect.DeepEqual(runTypes, wantRuns) {
		t.Errorf("run components = %v", runTypes)
	}
	for _, n := range []string{"LaravelPint", "PhpStanOptionsConfiguration", "PhpUnit", "VcsDirectoryMappings", "PhpIncludePathManager", "PhpTestFrameworkVersionCache", "SomeThirdPartyComponent"} {
		c, ok := comps[n]
		if !ok || c.Quiet || c.Scope != ScopeProject {
			t.Errorf("component %s = %+v", n, c)
		}
	}
	if c := comps["Git.Settings"]; !c.Quiet || c.File != ".idea/workspace.xml" {
		t.Errorf("workspace component = %+v", c)
	}
	if c := comps["LaravelPintOptionsConfiguration"]; c.Path(mustOption(t, c.Element, "configPath")) != "pint.json" {
		t.Errorf("component path = %q", c.Path(mustOption(t, c.Element, "configPath")))
	}
	if _, err := ScanProject(context.Background(), t.TempDir(), ProjectOptions{}); err == nil {
		t.Error("project without .idea accepted")
	}
}

func mustOption(t *testing.T, el *Element, name string) string {
	t.Helper()
	v, ok := el.Option(name)
	if !ok {
		t.Fatalf("no option %s", name)
	}
	return v
}

func coreType(t *testing.T, id string) run.ConfigType {
	t.Helper()
	for _, ct := range run.CoreTypes() {
		if ct.ID == id {
			return ct
		}
	}
	t.Fatalf("no core type %s", id)
	return run.ConfigType{}
}

func writeIdea(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, IdeaDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestProjectInterpreterKinds(t *testing.T) {
	php := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(php, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	php = filepath.ToSlash(php)
	for _, tc := range []struct {
		name, xml string
		check     func(t *testing.T, it Item)
	}{
		{"local", `<interpreter name="L" home="` + php + `"/>`, func(t *testing.T, it Item) {
			if change(t, it, KeyInterpreter).Value != "local:"+filepath.FromSlash(php) {
				t.Errorf("local = %+v", it)
			}
		}},
		{"local missing", `<interpreter name="L" home="/nonexistent/php"/>`, func(t *testing.T, it Item) {
			if it.Status != StatusSkipped {
				t.Errorf("missing local = %+v", it)
			}
		}},
		{"docker image", `<interpreter name="L" home="docker://php:8.3-cli/php"><remote_data DOCKER_REMOTE_PROJECT_PATH="/app"/></interpreter>`, func(t *testing.T, it Item) {
			if change(t, it, KeyInterpreter).Value != "docker:php:8.3-cli" || !reflect.DeepEqual(change(t, it, KeyDockerImages).Value, []string{"php:8.3-cli=/app"}) {
				t.Errorf("docker = %+v", it)
			}
		}},
		{"ssh", `<interpreter name="L" home="ssh://u@h:22/usr/bin/php"/>`, func(t *testing.T, it Item) {
			if it.Status != StatusUnsupported {
				t.Errorf("ssh = %+v", it)
			}
		}},
		{"compose without file", `<interpreter name="L" home="docker-compose://[$PROJECT_DIR$/docker-compose.yml]:app/php"/>`, func(t *testing.T, it Item) {
			if it.Status != StatusSkipped || !strings.Contains(it.Detail, "Compose") {
				t.Errorf("compose without file = %+v", it)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeIdea(t, map[string]string{"php.xml": `<project><component name="PhpInterpreters"><interpreters>` + tc.xml + `</interpreters></component></project>`})
			plan, err := ScanProject(context.Background(), dir, ProjectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, item(t, plan.Items, "PHP interpreter L"))
		})
	}
	// A named interpreter that is defined nowhere.
	dir := writeIdea(t, map[string]string{"workspace.xml": `<project><component name="PhpWorkspaceProjectConfiguration" interpreter_name="ghost"/></project>`})
	plan, _ := ScanProject(context.Background(), dir, ProjectOptions{})
	if it := item(t, plan.Items, "PHP interpreter"); it.Status != StatusSkipped {
		t.Errorf("ghost = %+v", it)
	}
	// Compose: unknown service, no service.
	dir = writeIdea(t, map[string]string{"php.xml": `<project><component name="PhpInterpreters"><interpreters><interpreter name="C" home="docker-compose://[$PROJECT_DIR$/docker-compose.yml]:web/php"/></interpreters></component></project>`})
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = ScanProject(context.Background(), dir, ProjectOptions{})
	if it := item(t, plan.Items, "PHP interpreter C"); it.Status != StatusSkipped || !strings.Contains(it.Detail, `"web"`) {
		t.Errorf("unknown service = %+v", it)
	}
	in := ParseInterpreter(&Element{Name: "interpreter", Attrs: nil}, Macros{})
	if in.Kind != "local" || in.Path != "" {
		t.Errorf("empty interpreter = %+v", in)
	}
}

func TestProjectLanguageLevelAndScripts(t *testing.T) {
	dir := writeIdea(t, map[string]string{
		"php.xml": `<project><component name="PhpProjectSharedConfiguration" php_language_level="nonsense"/></project>`,
		"runConfigurations/x.xml": `<component name="ProjectRunConfigurationManager">
<configuration name="No file" type="PhpLocalRunConfigurationType"/>
<configuration name="Sub" type="PhpLocalRunConfigurationType" path="$PROJECT_DIR$/bin/x.php"><CommandLine workingDirectory="$PROJECT_DIR$/bin"/></configuration>
</component>`,
		"codeStyles/codeStyleConfig.xml": `<component><state><option name="USE_PER_PROJECT_SETTINGS" value="false"/></state></component>`,
	})
	plan, err := ScanProject(context.Background(), dir, ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if it := item(t, plan.Items, "PHP language level"); it.Status != StatusUnsupported {
		t.Errorf("bad level = %+v", it)
	}
	if it := item(t, plan.Items, "Run configuration No file"); it.Status != StatusSkipped {
		t.Errorf("no file = %+v", it)
	}
	sub := item(t, plan.Items, "Run configuration Sub").Changes[0].RunConfig
	if sub.Values["workingDir"] != "bin" || sub.Values["file"] != "bin/x.php" {
		t.Errorf("sub = %+v", sub.Values)
	}
	for _, it := range plan.Items {
		if it.Name == "Project code style" {
			t.Error("code style imported without per-project settings")
		}
	}
	if !HasProjectConfig(dir) || HasProjectConfig(t.TempDir()) {
		t.Error("HasProjectConfig")
	}
	env := RunEnv(&Element{Children: []*Element{{Name: "env", Attrs: nil}}})
	if env != nil {
		t.Errorf("env without name = %v", env)
	}
}
