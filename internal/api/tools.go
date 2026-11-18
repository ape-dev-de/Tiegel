package api

import (
	"context"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// External tools (AP17.2, D412–D415): run configurations of the core type
// run.TypeExternalTool. The backend expands their macros with the editor
// context of run.start (RunStartParams.Context), lists the macros
// (run.macros), offers every tool as a bindable keymap action
// (HookKeymapActions, ID ExternalToolAction) and synchronises the files
// after a run with run.Spec.SyncFiles.

// ExternalToolActionPrefix prefixes the keymap action of an external tool;
// the rest is the configuration ID (stable across renames).
const ExternalToolActionPrefix = "externalTool."

// syncAfterRunTimeout bounds the file synchronisation after a run.
const syncAfterRunTimeout = 30 * time.Second

// HookKeymapActions collects bindable actions that are not in the static
// catalog (keymap.Defs), e.g. one per external tool. The kernel resolves
// keymap.* with them, so their chords can be set, listed and checked for
// conflicts like any other action; clients add the same definitions to
// their scheme (ExternalToolDefs for the tools).
var HookKeymapActions = hooks.NewFilter[[]keymap.Def]("keymap.actions")

// ExternalToolAction returns the keymap action ID of external tool cfgID.
func ExternalToolAction(cfgID string) keymap.Action {
	return keymap.Action(ExternalToolActionPrefix + cfgID)
}

// ExternalToolDefs returns the keymap definitions of the external tools
// among configs (no default chords; the user assigns them).
func ExternalToolDefs(configs []run.Config) []keymap.Def {
	var out []keymap.Def
	for _, c := range configs {
		if c.Type != run.TypeExternalTool || c.ID == "" {
			continue
		}
		out = append(out, keymap.Def{ID: ExternalToolAction(c.ID), Title: "External Tools: " + c.Name, Context: keymap.CtxGlobal})
	}
	return out
}

// RunMacrosParams are the params of "run.macros".
type RunMacrosParams struct {
	// Context previews the values for this editor context (optional).
	Context *run.MacroContext `json:"context,omitempty"`
}

// RunMacroInfo is a macro with its preview value.
type RunMacroInfo struct {
	run.Macro
	// Value is the expansion for the given context; Error explains why
	// there is none (a file macro without file).
	Value string `json:"value"`
	Error string `json:"error,omitempty"`
}

func (a *App) registerExternalTools() {
	mustHandle(a, "run.macros", "Macros of external tools ($FilePath$, $LineNumber$, $SelectedText$, …) with their values for an editor context", a.runMacros)
	HookKeymapActions.On(a.hooks, -1000, func(_ context.Context, l []keymap.Def) ([]keymap.Def, error) {
		if a.ProjectRoot() == "" {
			return l, nil
		}
		list, err := a.storedRunConfigs()
		if err != nil {
			return l, nil // no tools without readable configurations
		}
		return append(ExternalToolDefs(list), l...), nil
	}, hooks.WithOwner("kernel"))
}

func (a *App) runMacros(_ context.Context, p RunMacrosParams) ([]RunMacroInfo, error) {
	root := a.ProjectRoot()
	out := []RunMacroInfo{}
	for _, m := range run.Macros() {
		info := RunMacroInfo{Macro: m}
		v, err := run.ExpandMacros(m.Name, root, p.Context)
		if err != nil {
			info.Error = "needs a current file"
		} else {
			info.Value = v
		}
		out = append(out, info)
	}
	return out, nil
}

// keymapExtras returns the dynamic keymap actions (HookKeymapActions).
func (a *App) keymapExtras() []keymap.Def {
	defs, err := HookKeymapActions.Apply(context.Background(), a.hooks, nil)
	if err != nil {
		a.log.Warn("keymap actions", "err", err)
	}
	return defs
}

// toolTemplate renders the command of an external tool with its macros
// unexpanded (preview when they need a file).
func toolTemplate(c run.Config) string {
	return strings.TrimSpace(c.String(run.ToolFieldProgram, "") + " " + c.String(run.ToolFieldArgs, ""))
}

// syncAfterRun rescans the project and reconciles open documents after a
// run with SyncFiles ("Synchronize files after execution"), unless the
// App closes or the run belongs to another project.
func (a *App) syncAfterRun(i run.Info) {
	select {
	case <-a.closing:
		return
	default:
	}
	if i.Project != "" && i.Project != a.ProjectRoot() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncAfterRunTimeout)
	defer cancel()
	if _, err := a.watchRefresh(ctx, struct{}{}); err != nil {
		a.log.Warn("sync after run", "run", i.ID, "err", err)
	}
}
