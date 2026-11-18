package ideimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// Setting keys written by the core mappings that are declared outside
// the packages this one may import (the kernel and php-runtime; tests
// there check that the values match).
const (
	KeyInterpreter        = "php-runtime.interpreter"
	KeyMappings           = "php-runtime.mappings"
	KeyExecMode           = "php-runtime.execMode"
	KeyDockerImages       = "php-runtime.dockerImages"
	KeyPHPVersionOverride = "php.versionOverride"
	KeyRecentProjects     = "ui.recentProjects"
)

// Events a Change can ask the kernel to publish after writing.
const (
	EventInterpreter = "interpreter"
	EventMappings    = "mappings"
	EventPHPVersion  = "phpVersion"
)

// maxRecent bounds the imported recent projects (the GUI keeps 20).
const maxRecent = 20

// AppOptions configure ScanApp.
type AppOptions struct {
	// GOOS decides the default keymap preset ("" = the keymap's base).
	GOOS string
	// Home resolves $USER_HOME$.
	Home string
}

// appFile is a parsed file of the application configuration.
type appFile struct {
	rel  string // e.g. "options/editor-font.xml"
	root *Element
}

// appScan collects the state of one ScanApp.
type appScan struct {
	src    Source
	opts   AppOptions
	macros Macros
	plan   Plan
	comps  map[string]appComp // component name → first occurrence
}

type appComp struct {
	file string
	el   *Element
}

// ScanApp reads the application configuration src and maps the core
// settings: fonts, theme, editor, code style, terminal, general
// behaviour, keymap, recent projects (area AreaRecent, opt-in) and
// external tools. Components of options/*.xml the core does not know are
// returned (quiet) for the plugins.
func ScanApp(src Source, opts AppOptions) (Plan, error) {
	s := &appScan{src: src, opts: opts, macros: Macros{UserHome: opts.Home, ConfigDir: src.Dir}, comps: map[string]appComp{}}
	files, err := readXMLDir(src.Dir, "options")
	if err != nil {
		return Plan{}, err
	}
	for _, f := range files {
		for _, c := range f.root.Components() {
			name := c.Attr("name")
			if _, dup := s.comps[name]; !dup {
				s.comps[name] = appComp{file: f.rel, el: c}
			}
		}
	}
	s.fonts()
	s.appearance()
	s.editorSettings()
	s.codeStyle()
	s.terminal()
	s.general()
	s.keymap()
	s.recent()
	s.interpreters()
	s.externalTools()
	for _, f := range files {
		for _, c := range f.root.Components() {
			name := c.Attr("name")
			if coreAppComponents[name] || s.comps[name].el != c {
				continue
			}
			s.plan.Components = append(s.plan.Components, Component{
				Kind: KindComponent, Name: name, File: f.rel, Scope: ScopeGlobal,
				Element: c, Macros: s.macros, Quiet: true,
			})
		}
	}
	return s.plan, nil
}

// coreAppComponents are the application components ScanApp maps itself.
var coreAppComponents = map[string]bool{
	"DefaultFont": true, "ConsoleFont": true, "TerminalFontOptions": true,
	"LafManager": true, "EditorColorsManagerImpl": true, "UISettings": true,
	"EditorSettings": true, "CodeStyleSchemeSettings": true,
	"TerminalOptionsProvider": true, "AdvancedSettings": true,
	"GeneralSettings": true, "KeymapManager": true,
	"RecentProjectsManager": true, "RecentDirectoryProjectsManager": true,
	"PhpInterpreters": true,
}

func (s *appScan) comp(name string) (appComp, bool) {
	c, ok := s.comps[name]
	return c, ok && c.el != nil
}

// readXMLDir parses the *.xml files of dir/sub (sorted by name; files
// that are no XML are skipped).
func readXMLDir(dir, sub string) ([]appFile, error) {
	entries, err := os.ReadDir(filepath.Join(dir, sub))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ideimport: %w", err)
	}
	var out []appFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".xml") {
			continue
		}
		root, err := ParseFile(filepath.Join(dir, sub, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, appFile{rel: sub + "/" + e.Name(), root: root})
	}
	return out, nil
}

// parseFloat reads a number ("13", "13.5").
func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil && f > 0
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// fontOptions reads family, size and line spacing of a font component.
func fontOptions(el *Element) (family string, size, spacing float64) {
	family, _ = el.Option("FONT_FAMILY")
	if v, ok := el.Option("FONT_SIZE_2D"); ok {
		size, _ = parseFloat(v)
	}
	if size == 0 {
		if v, ok := el.Option("FONT_SIZE"); ok {
			size, _ = parseFloat(v)
		}
	}
	if v, ok := el.Option("LINE_SPACING"); ok {
		spacing, _ = parseFloat(v)
	}
	return family, size, spacing
}

// clampSpacing keeps a line spacing in Tiegel's range (1–3).
func clampSpacing(v float64) float64 { return min(max(v, 1), 3) }

func (s *appScan) fonts() {
	if c, ok := s.comp("DefaultFont"); ok {
		family, size, spacing := fontOptions(c.el)
		var ch []Change
		var parts []string
		if family != "" {
			ch = append(ch, Setting(settings.KeyEditorFontFamily, ScopeGlobal, family))
			parts = append(parts, family)
		}
		if size > 0 {
			ch = append(ch, Setting(settings.KeyEditorFontSize, ScopeGlobal, size))
			parts = append(parts, fmt.Sprintf("%g pt", size))
		}
		if spacing > 0 {
			ch = append(ch, Setting(settings.KeyEditorLineHeight, ScopeGlobal, clampSpacing(spacing)))
			parts = append(parts, fmt.Sprintf("line height %g", clampSpacing(spacing)))
		}
		it := Item{Area: AreaEditor, Name: "Editor font", Source: c.file, Detail: strings.Join(parts, ", "), Changes: ch}
		if len(ch) == 0 {
			it.Status, it.Detail = StatusSkipped, "no font options"
		}
		s.plan.add(it)
	}
	term, hasTerm := s.comp("TerminalFontOptions")
	if hasTerm {
		family, size, _ := fontOptions(term.el)
		it := Item{Area: AreaTerminal, Name: "Terminal font", Source: term.file}
		if size > 0 {
			it.Changes = []Change{Setting(terminal.KeyFontSize, ScopeGlobal, size)}
			it.Detail = fmt.Sprintf("%g pt", size)
		} else {
			it.Status, it.Detail = StatusSkipped, "no font size"
		}
		if family != "" {
			it.Detail += "; family " + family + " not supported (the terminal uses the editor font)"
		}
		s.plan.add(it)
	}
	if c, ok := s.comp("ConsoleFont"); ok {
		_, size, _ := fontOptions(c.el)
		it := Item{Area: AreaTerminal, Name: "Console font", Source: c.file}
		switch {
		case hasTerm || size == 0:
			it.Status, it.Detail = StatusUnsupported, "the Run console uses the editor font"
		default:
			it.Changes = []Change{Setting(terminal.KeyFontSize, ScopeGlobal, size)}
			it.Detail = fmt.Sprintf("%g pt, used as terminal font size (the Run console uses the editor font)", size)
		}
		s.plan.add(it)
	}
}

// themeOf maps a look-and-feel or scheme name to light or dark.
func themeOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case n == "":
		return ""
	case strings.Contains(n, "dark") || strings.Contains(n, "darcula") || strings.Contains(n, "contrast"):
		return "dark"
	case strings.Contains(n, "light") || strings.Contains(n, "intellij") || strings.Contains(n, "default"):
		return "light"
	}
	return ""
}

func (s *appScan) appearance() {
	theme, src, detail := "", "", ""
	if c, ok := s.comp("LafManager"); ok {
		src = c.file
		if v, _ := parseBool(c.el.Attr("autodetect")); v {
			theme, detail = "system", "follow the system"
		} else if laf := c.el.Child("laf"); laf != nil {
			name := laf.Attr("themeId")
			if name == "" {
				name = laf.Attr("class-name")
			}
			theme, detail = themeOf(name), name
		}
	}
	if theme == "" {
		if c, ok := s.comp("EditorColorsManagerImpl"); ok {
			name := c.el.Child("global_color_scheme").Attr("name")
			src, theme, detail = c.file, themeOf(name), "color scheme "+name
		}
	}
	if src != "" {
		it := Item{Area: AreaAppearance, Name: "Theme", Source: src}
		if theme == "" {
			it.Status, it.Detail = StatusUnsupported, detail+": neither light nor dark"
		} else {
			it.Changes = []Change{Setting(settings.KeyTheme, ScopeGlobal, theme)}
			it.Detail = theme + " (" + detail + ")"
			if theme != "system" {
				it.Detail += "; Tiegel has its own palette, only light/dark is taken"
			}
		}
		s.plan.add(it)
	}
	if c, ok := s.comp("UISettings"); ok {
		override, _ := c.el.Option("OVERRIDE_NONIDEA_LAF_FONTS")
		on, _ := parseBool(override)
		v, _ := c.el.Option("FONT_SIZE")
		size, okSize := parseFloat(v)
		if on && okSize {
			s.plan.add(Item{Area: AreaAppearance, Name: "UI font size", Source: c.file, Detail: fmt.Sprintf("%g", size),
				Changes: []Change{Setting(settings.KeyUIFontSize, ScopeGlobal, size)}})
		}
	}
}

func (s *appScan) editorSettings() {
	c, ok := s.comp("EditorSettings")
	if !ok {
		return
	}
	if use, ok := c.el.Option("USE_SOFT_WRAPS"); ok {
		masks, _ := c.el.Option("SOFT_WRAP_FILE_MASKS")
		it := Item{Area: AreaEditor, Name: "Soft wrap", Source: c.file}
		var list []string
		for _, m := range strings.Split(masks, ";") {
			if m = strings.TrimSpace(m); m != "" {
				list = append(list, m)
			}
		}
		switch {
		case !strings.Contains(use, "MAIN_EDITOR"):
			it.Changes, it.Detail = []Change{Setting(settings.KeySoftWrap, ScopeGlobal, settings.SoftWrapOff)}, "off"
		case len(list) == 0 || (len(list) == 1 && list[0] == "*"):
			it.Changes, it.Detail = []Change{Setting(settings.KeySoftWrap, ScopeGlobal, settings.SoftWrapAll)}, "all files"
		default:
			it.Changes = []Change{
				Setting(settings.KeySoftWrap, ScopeGlobal, settings.SoftWrapFiles),
				Setting(settings.KeySoftWrapFiles, ScopeGlobal, list),
			}
			it.Detail = strings.Join(list, ", ")
		}
		s.plan.add(it)
	}
	if v, ok := c.el.Option("IS_WHITESPACES_SHOWN"); ok {
		shown, _ := parseBool(v)
		mode := settings.WhitespaceNone
		if shown {
			mode = settings.WhitespaceAll
			off := func(name string) bool { v, ok := c.el.Option(name); b, _ := parseBool(v); return ok && !b }
			sel, _ := c.el.Option("IS_SELECTION_WHITESPACES_SHOWN")
			switch {
			case func() bool { b, _ := parseBool(sel); return b }():
				mode = settings.WhitespaceSelection
			case off("IS_LEADING_WHITESPACES_SHOWN") && off("IS_INNER_WHITESPACES_SHOWN"):
				mode = settings.WhitespaceTrailing
			}
		}
		s.plan.add(Item{Area: AreaEditor, Name: "Show whitespace", Source: c.file, Detail: mode,
			Changes: []Change{Setting(settings.KeyShowWhitespace, ScopeGlobal, mode)}})
	}
}

// CodeStyle is what Tiegel takes from a code style scheme.
type CodeStyle struct {
	IndentSize  int
	UseTabs     *bool
	RightMargin int
}

// ParseCodeStyle reads indent size, tabs and right margin of a
// <code_scheme> (the PHP language section wins over the general one).
func ParseCodeStyle(scheme *Element) CodeStyle {
	var cs CodeStyle
	readIndent := func(io *Element) {
		if io == nil {
			return
		}
		if v, ok := io.Option("INDENT_SIZE"); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cs.IndentSize = n
			}
		}
		if v, ok := io.Option("USE_TAB_CHARACTER"); ok {
			if b, ok := parseBool(v); ok {
				cs.UseTabs = &b
			}
		}
	}
	margin := func(el *Element) {
		if v, ok := el.Option("RIGHT_MARGIN"); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				cs.RightMargin = n
			}
		}
	}
	margin(scheme)
	if o := scheme.OptionElement("OTHER_INDENT_OPTIONS"); o != nil {
		readIndent(o.Child("value"))
	}
	for _, l := range scheme.All("codeStyleSettings") {
		if l.Attr("language") == "PHP" {
			margin(l)
			readIndent(l.Child("indentOptions"))
		}
	}
	return cs
}

// codeStyleItem turns a code style into an item for scope.
func codeStyleItem(cs CodeStyle, name, source, scope string) Item {
	it := Item{Area: AreaEditor, Name: name, Source: source}
	var parts []string
	if cs.IndentSize > 0 {
		it.Changes = append(it.Changes, Setting(settings.KeyEditorTabWidth, scope, cs.IndentSize))
		parts = append(parts, fmt.Sprintf("indent %d", cs.IndentSize))
	}
	if cs.UseTabs != nil {
		it.Changes = append(it.Changes, Setting(settings.KeyEditorInsertSpace, scope, !*cs.UseTabs))
		if *cs.UseTabs {
			parts = append(parts, "tabs")
		} else {
			parts = append(parts, "spaces")
		}
	}
	if cs.RightMargin > 0 {
		it.Changes = append(it.Changes, Setting(settings.KeyRightMargin, scope, cs.RightMargin))
		parts = append(parts, fmt.Sprintf("right margin %d", cs.RightMargin))
	}
	it.Detail = strings.Join(parts, ", ")
	if len(it.Changes) == 0 {
		it.Status, it.Detail = StatusSkipped, "no indentation or margin set (defaults)"
	} else {
		it.Detail += "; other code style rules come from the project formatter"
	}
	return it
}

func (s *appScan) codeStyle() {
	name := "Default"
	src := "codestyles/Default.xml"
	if c, ok := s.comp("CodeStyleSchemeSettings"); ok {
		if v, ok := c.el.Option("CURRENT_SCHEME_NAME"); ok && v != "" {
			name = v
		}
	}
	src = "codestyles/" + name + ".xml"
	root, err := ParseFile(filepath.Join(s.src.Dir, filepath.FromSlash(src)))
	if err != nil {
		return
	}
	scheme := root
	if scheme.Name != "code_scheme" {
		scheme = root.Child("code_scheme")
	}
	if scheme == nil {
		return
	}
	s.plan.add(codeStyleItem(ParseCodeStyle(scheme), "Code style "+name, src, ScopeGlobal))
}

// firstOption returns the first existing option of names.
func firstOption(el *Element, names ...string) (string, bool) {
	for _, n := range names {
		if v, ok := el.Option(n); ok {
			return v, true
		}
	}
	return "", false
}

func (s *appScan) terminal() {
	if c, ok := s.comp("TerminalOptionsProvider"); ok {
		if v, ok := firstOption(c.el, "myShellPath", "shellPath"); ok && strings.TrimSpace(v) != "" {
			words, err := run.SplitWords(s.macros.Expand(v))
			it := Item{Area: AreaTerminal, Name: "Terminal shell", Source: c.file, Detail: v}
			if err != nil || len(words) == 0 {
				it.Status, it.Detail = StatusSkipped, "cannot read shell command "+v
			} else {
				it.Changes = []Change{Setting(terminal.KeyShell, ScopeGlobal, words[0])}
				if len(words) > 1 {
					it.Changes = append(it.Changes, Setting(terminal.KeyShellArgs, ScopeGlobal, words[1:]))
				}
				// A path (not a bare name looked up in PATH) must exist
				// here; "/bin/zsh" is no absolute path on Windows.
				if _, err := os.Stat(words[0]); err != nil && strings.ContainsAny(words[0], `/\`) {
					it.Status, it.Detail = StatusSkipped, words[0]+" does not exist here"
					it.Changes = nil
				}
			}
			s.plan.add(it)
		}
		boolOpt := func(name string, key string, names ...string) {
			if v, ok := firstOption(c.el, names...); ok {
				if b, ok := parseBool(v); ok {
					s.plan.add(Item{Area: AreaTerminal, Name: name, Source: c.file, Detail: strconv.FormatBool(b),
						Changes: []Change{Setting(key, ScopeGlobal, b)}})
				}
			}
		}
		boolOpt("Terminal keys override IDE shortcuts", terminal.KeyOverrideKeys, "myOverrideIdeShortcuts", "overrideIdeShortcuts")
		boolOpt("Close terminal tab when the shell exits", terminal.KeyCloseOnExit, "myCloseSessionOnLogout", "closeSessionOnLogout")
		boolOpt("Terminal: Option as Meta", terminal.KeyOptionAsMeta, "useOptionAsMetaKey", "myUseOptionAsMetaKey")
	}
	if c, ok := s.comp("AdvancedSettings"); ok {
		for _, e := range c.el.Find("entry") {
			if strings.Contains(e.Attr("key"), "option.as.meta") {
				if b, ok := parseBool(e.Attr("value")); ok {
					s.plan.add(Item{Area: AreaTerminal, Name: "Terminal: Option as Meta", Source: c.file, Detail: strconv.FormatBool(b),
						Changes: []Change{Setting(terminal.KeyOptionAsMeta, ScopeGlobal, b)}})
				}
			}
		}
	}
}

func (s *appScan) general() {
	c, ok := s.comp("GeneralSettings")
	if !ok {
		return
	}
	if v, ok := c.el.Option("confirmOpenNewProject2"); ok {
		mode := map[string]string{"-1": settings.OpenProjectAsk, "0": settings.OpenProjectNew, "1": settings.OpenProjectThis, "2": settings.OpenProjectThis}[strings.TrimSpace(v)]
		it := Item{Area: AreaGeneral, Name: "Open project in", Source: c.file, Detail: mode}
		if mode == "" {
			it.Status, it.Detail = StatusUnsupported, "unknown value "+v
		} else {
			it.Changes = []Change{Setting(settings.KeyOpenProjectIn, ScopeGlobal, mode)}
		}
		s.plan.add(it)
	}
	if v, ok := c.el.Option("processCloseConfirmation"); ok {
		mode := map[string]string{"ASK": run.CloseAsk, "TERMINATE": run.CloseTerminate, "DISCONNECT": run.CloseKeep}[strings.ToUpper(strings.TrimSpace(v))]
		it := Item{Area: AreaGeneral, Name: "Running processes on close", Source: c.file, Detail: mode}
		if mode == "" {
			it.Status, it.Detail = StatusUnsupported, "unknown value "+v
		} else {
			it.Changes = []Change{Setting(run.KeyConfirmClose, ScopeGlobal, mode)}
		}
		s.plan.add(it)
	}
}

func (s *appScan) recent() {
	c, ok := s.comp("RecentProjectsManager")
	if !ok {
		c, ok = s.comp("RecentDirectoryProjectsManager")
	}
	if !ok {
		return
	}
	type proj struct {
		path  string
		stamp int64
		order int
	}
	var list []proj
	seen := map[string]bool{}
	addPath := func(raw string, stamp int64) {
		p := s.macros.Path(raw)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		list = append(list, proj{path: p, stamp: stamp, order: len(list)})
	}
	if info := c.el.OptionElement("additionalInfo"); info != nil {
		for _, e := range info.Find("entry") {
			var stamp int64
			if meta := e.Find("RecentProjectMetaInfo"); len(meta) > 0 {
				v, _ := meta[0].Option("activationTimestamp")
				stamp, _ = strconv.ParseInt(v, 10, 64)
			}
			addPath(e.Attr("key"), stamp)
		}
	}
	if paths := c.el.OptionElement("recentPaths"); paths != nil {
		for _, e := range paths.Find("option") {
			addPath(e.Attr("value"), 0)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].stamp != list[j].stamp {
			return list[i].stamp > list[j].stamp
		}
		return list[i].order < list[j].order
	})
	var keep, missing []string
	for _, p := range list {
		if isDir(p.path) {
			if len(keep) < maxRecent {
				keep = append(keep, p.path)
			}
		} else {
			missing = append(missing, p.path)
		}
	}
	it := Item{Area: AreaRecent, Name: "Recent projects", Source: c.file}
	if len(keep) == 0 {
		it.Status, it.Detail = StatusSkipped, "no recent project exists on this computer"
	} else {
		it.Detail = fmt.Sprintf("%d projects", len(keep))
		it.Changes = []Change{{Key: KeyRecentProjects, Scope: ScopeGlobal, Value: keep, Merge: MergeList}}
	}
	if len(missing) > 0 {
		it.Detail += fmt.Sprintf("; %d no longer exist and are left out", len(missing))
	}
	s.plan.add(it)
}

// interpreters reports the application-wide interpreter list; the
// selection is per project (ScanProject resolves names against it).
func (s *appScan) interpreters() {
	c, ok := s.comp("PhpInterpreters")
	if !ok {
		return
	}
	n := len(c.el.Find("interpreter"))
	s.plan.add(Item{Area: AreaInterpreter, Name: "PHP interpreters", Source: c.file, Status: StatusSkipped,
		Detail: fmt.Sprintf("%d interpreters; Tiegel finds local ones itself, the project's choice is imported with the project", n)})
}

// externalTools returns the tools of tools/*.xml as components (no core
// run type; a plugin may take them).
func (s *appScan) externalTools() {
	files, err := readXMLDir(s.src.Dir, "tools")
	if err != nil {
		return
	}
	for _, f := range files {
		for _, t := range f.root.Find("tool") {
			s.plan.Components = append(s.plan.Components, Component{
				Kind: KindExternalTool, Name: t.Attr("name"), File: f.rel, Scope: ScopeGlobal,
				Element: t, Macros: s.macros,
			})
		}
	}
}

// ExternalTool is the neutral form of an external tool.
type ExternalTool struct {
	Name       string `json:"name"`
	Program    string `json:"program"`
	Args       string `json:"args,omitempty"`
	WorkingDir string `json:"workingDir,omitempty"`
}

// ParseExternalTool reads a <tool> element of tools/*.xml.
func ParseExternalTool(t *Element) ExternalTool {
	et := ExternalTool{Name: t.Attr("name")}
	if ex := t.Child("exec"); ex != nil {
		et.Program, _ = ex.Option("COMMAND")
		et.Args, _ = ex.Option("PARAMETERS")
		et.WorkingDir, _ = ex.Option("WORKING_DIRECTORY")
	}
	return et
}
