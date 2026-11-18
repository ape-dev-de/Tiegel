package ideimport

import (
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/run"
)

// Areas group the items of an import; the user selects areas in the
// preview (checkboxes) and with --areas.
const (
	AreaGeneral     = "general"
	AreaAppearance  = "appearance"
	AreaEditor      = "editor"
	AreaKeymap      = "keymap"
	AreaTerminal    = "terminal"
	AreaTools       = "externalTools"
	AreaRecent      = "recentProjects"
	AreaInterpreter = "interpreter"
	AreaRun         = "runConfigurations"
	AreaPlugins     = "plugins"
)

// AreaInfo describes an area.
type AreaInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Default is whether the area is selected without a choice (recent
	// projects are opt-in).
	Default bool `json:"default"`
}

// Areas lists the areas in display order.
var Areas = []AreaInfo{
	{AreaGeneral, "General", true},
	{AreaAppearance, "Appearance", true},
	{AreaEditor, "Editor", true},
	{AreaKeymap, "Keymap", true},
	{AreaTerminal, "Terminal", true},
	{AreaTools, "External tools", true},
	{AreaRecent, "Recent projects", false},
	{AreaInterpreter, "PHP interpreter, language level, path mappings", true},
	{AreaRun, "Run configurations", true},
	{AreaPlugins, "Plugin settings", true},
}

// AreaLabel returns the label of area id (id itself when unknown).
func AreaLabel(id string) string {
	for _, a := range Areas {
		if a.ID == id {
			return a.Label
		}
	}
	return id
}

// Status is the state of an item.
type Status string

// Item states. A preview (dry run) reports Ready for what an import would
// change; an import turns them into Imported or Failed.
const (
	StatusReady       Status = "ready"
	StatusImported    Status = "imported"
	StatusSkipped     Status = "skipped"
	StatusUnsupported Status = "unsupported"
	StatusFailed      Status = "failed"
)

// Scopes of a Change (the settings store written).
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Merge modes of a Change.
const (
	// MergeSet replaces the stored value (default).
	MergeSet = ""
	// MergeList adds the entries of a string list value that are missing
	// in the stored list (after them).
	MergeList = "list"
	// MergeKeymap merges keymap override lines ("<action> <chord>…"):
	// imported lines replace stored lines of the same action.
	MergeKeymap = "keymap"
)

// Change is one write of an item: a setting or a run configuration.
type Change struct {
	// Key, Scope and Value describe a setting.
	Key   string `json:"key,omitempty"`
	Scope string `json:"scope,omitempty"`
	Value any    `json:"value,omitempty"`
	Merge string `json:"merge,omitempty"`
	// RunConfig is a run configuration of the project (matched by name:
	// an existing one with the same name is replaced, keeping its ID).
	RunConfig *run.Config `json:"runConfig,omitempty"`
	// Event, if set, is published after the change was written (e.g. the
	// interpreter changed); the kernel interprets it.
	Event string `json:"event,omitempty"`
	// Executable marks a setting whose value names a program Tiegel runs.
	// The kernel also knows this from the settings spec (Spec.Executable)
	// and the interpreter and run configuration types; plugins may set it
	// for undeclared keys. Such changes from a project's .idea need the
	// opt-in for programs from the repository.
	Executable bool `json:"executable,omitempty"`
}

// String describes the change for reports.
func (c Change) String() string {
	if c.RunConfig != nil {
		return fmt.Sprintf("run configuration %q (%s)", c.RunConfig.Name, c.RunConfig.Type)
	}
	return fmt.Sprintf("%s = %v (%s)", c.Key, c.Value, c.Scope)
}

// Item is one entry of the import report: what was found where, what it
// becomes in Tiegel and whether it is (or would be) imported.
type Item struct {
	Area string `json:"area"`
	// Name describes the item ("Editor font", "Run configuration Unit").
	Name string `json:"name"`
	// Source is the file it comes from, relative to the configuration
	// directory or the project (".idea/php.xml").
	Source string `json:"source"`
	Status Status `json:"status"`
	// Detail is the value or the reason (skipped, unsupported, failed).
	Detail string `json:"detail,omitempty"`
	// Plugin is the module that mapped the item (plugin components).
	Plugin  string   `json:"plugin,omitempty"`
	Changes []Change `json:"changes,omitempty"`
	// Executable is the program the item makes Tiegel run when it comes
	// from the project (or points into it). The kernel sets it; such an
	// item is imported only with the opt-in for programs from the
	// repository (D440).
	Executable string `json:"executable,omitempty"`
	// AppDefined marks an item of ScanProject whose program is defined
	// in the application configuration (the user's own interpreter list),
	// so only the choice comes from the project. The kernel honours it
	// only for the core's own items, never for plugin items.
	AppDefined bool `json:"-"`
}

// Setting returns a setting change.
func Setting(key, scope string, value any) Change {
	return Change{Key: key, Scope: scope, Value: value}
}

// Kinds of a Component.
const (
	// KindComponent is a <component name="…"> of a configuration file.
	KindComponent = "component"
	// KindRunConfiguration is a <configuration type="…"> (Name is the
	// type).
	KindRunConfiguration = "runConfiguration"
	// KindExternalTool is a <tool> of tools/*.xml (Name is the tool's
	// name, Element the tool).
	KindExternalTool = "externalTool"
)

// Component is a part of the configuration the core does not interpret
// itself. The kernel offers it to the plugins through the filter hook
// "import.ide.component"; a plugin that knows Name returns items for it.
type Component struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// File is the source file (as Item.Source).
	File string `json:"file"`
	// Scope is where its settings belong: ScopeGlobal for the
	// application configuration, ScopeProject for .idea/.
	Scope   string   `json:"scope"`
	Element *Element `json:"-"`
	Macros  Macros   `json:"-"`
	// Quiet components are not reported when no plugin takes them
	// (application option files and workspace.xml hold mostly UI state).
	Quiet bool `json:"quiet,omitempty"`
}

// Item returns an item for the component: area plugins, source File,
// status Ready.
func (c Component) Item(name, detail string, changes ...Change) Item {
	return Item{Area: AreaPlugins, Name: name, Source: c.File, Status: StatusReady, Detail: detail, Changes: changes}
}

// Setting returns a setting change in the component's scope.
func (c Component) Setting(key string, value any) Change { return Setting(key, c.Scope, value) }

// Path resolves a path value of the component (macros expanded) and
// returns it relative to the project when inside it ("" if unusable).
func (c Component) Path(v string) string {
	p := c.Macros.Path(v)
	if p == "" {
		return ""
	}
	return c.Macros.ProjectRel(p)
}

// Plan is the result of a scan: the items the core mapped and the
// components left for the plugins.
type Plan struct {
	Items      []Item      `json:"items"`
	Components []Component `json:"components,omitempty"`
}

func (p *Plan) add(it Item) {
	if it.Status == "" {
		it.Status = StatusReady
	}
	p.Items = append(p.Items, it)
}

// JoinDetail appends reason to a detail text ("; " separated).
func JoinDetail(detail, reason string) string {
	if detail == "" {
		return reason
	}
	return detail + "; " + reason
}
