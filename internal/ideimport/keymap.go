package ideimport

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// ActionMap maps action IDs of the reference IDE's keymap files to
// Tiegel's catalog actions (keymap.Defs). Several reference actions may
// map to one Tiegel action (their chords are combined).
var ActionMap = map[string]keymap.Action{
	// Caret movement and selection.
	"EditorLeft": keymap.MoveLeft, "EditorRight": keymap.MoveRight,
	"EditorUp": keymap.MoveUp, "EditorDown": keymap.MoveDown,
	"EditorPreviousWord": keymap.MoveWordLeft, "EditorNextWord": keymap.MoveWordRight,
	"EditorLineStart": keymap.MoveLineStart, "EditorLineEnd": keymap.MoveLineEnd,
	"EditorPageUp": keymap.MovePageUp, "EditorPageDown": keymap.MovePageDown,
	"EditorTextStart": keymap.MoveDocStart, "EditorTextEnd": keymap.MoveDocEnd,
	"EditorLeftWithSelection": keymap.SelectLeft, "EditorRightWithSelection": keymap.SelectRight,
	"EditorUpWithSelection": keymap.SelectUp, "EditorDownWithSelection": keymap.SelectDown,
	"EditorPreviousWordWithSelection": keymap.SelectWordLeft, "EditorNextWordWithSelection": keymap.SelectWordRight,
	"EditorLineStartWithSelection": keymap.SelectLineStart, "EditorLineEndWithSelection": keymap.SelectLineEnd,
	"EditorPageUpWithSelection": keymap.SelectPageUp, "EditorPageDownWithSelection": keymap.SelectPageDown,
	"EditorTextStartWithSelection": keymap.SelectDocStart, "EditorTextEndWithSelection": keymap.SelectDocEnd,
	"EditorCloneCaretAbove": keymap.AddCursorAbove, "EditorCloneCaretBelow": keymap.AddCursorBelow,
	"EditorEscape":         keymap.CollapseSelection,
	"$SelectAll":           keymap.SelectAll,
	"EditorSelectWord":     keymap.ExpandSelection,
	"EditorUnSelectWord":   keymap.ShrinkSelection,
	"SelectNextOccurrence": keymap.AddNextOccurrence, "SelectAllOccurrences": keymap.SelectAllOccurrences,
	// Editing.
	"EditorEnter": keymap.Newline, "EditorBackSpace": keymap.DeleteBackward, "EditorDelete": keymap.DeleteForward,
	"EditorDeleteToWordStart": keymap.DeleteWordBackward, "EditorDeleteToWordEnd": keymap.DeleteWordForward,
	"EditorDeleteLine":      keymap.DeleteLine,
	"EditorIndentSelection": keymap.Indent, "EditorIndentLineOrSelection": keymap.Indent,
	"EditorUnindentSelection": keymap.Outdent,
	"EditorDuplicate":         keymap.DuplicateLines,
	"MoveLineUp":              keymap.MoveLinesUp, "MoveLineDown": keymap.MoveLinesDown,
	"MoveStatementUp": keymap.MoveLinesUp, "MoveStatementDown": keymap.MoveLinesDown,
	"CommentByLineComment": keymap.ToggleComment,
	"$Undo":                keymap.Undo, "$Redo": keymap.Redo,
	"$Copy": keymap.Copy, "$Cut": keymap.Cut, "$Paste": keymap.Paste,
	"EditorJoinLines": keymap.JoinLines, "EditorToggleCase": keymap.ToggleCase,
	"PasteMultiple": keymap.PasteFromHistory,
	// Folding and search in the editor.
	"CollapseRegion": keymap.FoldCollapse, "ExpandRegion": keymap.FoldExpand,
	"CollapseAllRegions": keymap.FoldCollapseAll, "ExpandAllRegions": keymap.FoldExpandAll,
	"FindNext": keymap.FindNext, "FindPrevious": keymap.FindPrevious,
	"Find": keymap.Find, "Replace": keymap.Replace,
	"GotoLine": keymap.GotoLine, "SaveAll": keymap.Save,
	// Application, view and tool windows.
	"ShowSettings": "app.settings", "GotoAction": "app.findAction",
	"CopyPaths": "file.copyPath", "CopyContentRootPath": "file.copyRelativePath",
	"HideAllWindows":           "view.maximizeEditor",
	"EditorIncreaseFontSize":   "view.zoomIn",
	"EditorDecreaseFontSize":   "view.zoomOut",
	"SelectInProjectView":      "view.selectInProject",
	"EditorToggleUseSoftWraps": keymap.ViewSoftWrap, "EditorToggleShowWhitespaces": keymap.ViewShowWhitespace,
	"ActivateCommitToolWindow": "view.commit", "ActivateProjectToolWindow": "view.project",
	"ActivateFindToolWindow": keymap.ViewFind, "ActivateDebugToolWindow": "view.debug",
	"ActivateProblemsViewToolWindow": "view.problems", "ActivateStructureToolWindow": "view.structure",
	"ActivateServicesToolWindow": "view.services", "ActivateVersionControlToolWindow": "view.git",
	"ActivateTerminalToolWindow": "view.terminal", "ActivateRunToolWindow": "view.run",
	// Navigation.
	"GotoFile": "file.quickOpen", "RecentFiles": "file.recent", "RecentLocations": keymap.NavRecentLocations,
	"Back": keymap.NavBack, "Forward": keymap.NavForward, "JumpToLastChange": keymap.NavLastEdit,
	"GotoClass": "navigate.gotoClass", "GotoSymbol": "navigate.gotoSymbol", "SearchEverywhere": "navigate.searchEverywhere",
	"FindInPath": "find.inPath", "ReplaceInPath": "find.replaceInPath",
	"GotoDeclaration": "navigate.definition", "GotoImplementation": "navigate.implementation",
	"GotoSuperMethod": "navigate.super", "TypeHierarchy": "navigate.typeHierarchy",
	"FindUsages": "navigate.usages", "FileStructurePopup": keymap.NavFileStructure,
	"GotoNextError": keymap.NavNextError, "GotoPreviousError": keymap.NavPreviousError,
	"GotoTest": "navigate.gotoTest",
	// Code.
	"RenameElement": "refactor.rename", "ReformatCode": "code.reformat",
	"ShowIntentionActions": "code.contextActions", "CodeCompletion": "editor.complete",
	"ParameterInfo": keymap.ParameterInfo, "QuickJavaDoc": keymap.QuickDocumentation,
	// Window and tabs.
	"NextSplitter": "window.nextGroup", "PrevSplitter": "window.previousGroup",
	"NextTab": "tab.next", "PreviousTab": "tab.previous", "CloseContent": "tab.close",
	// Version control.
	"CheckinProject": "git.commit", "Vcs.Push": "git.push", "ChangesView.Revert": "git.rollback",
	"Vcs.UpdateProject": "git.updateProject", "ChangesView.Diff": "git.showDiff",
	// Debugger, run and tests.
	"Resume": "debug.resume", "StepOver": "debug.stepOver", "StepInto": "debug.stepInto",
	"StepOut": "debug.stepOut", "Stop": "debug.stop", "ToggleLineBreakpoint": "debug.toggleBreakpoint",
	"Run": "run.run", "RunClass": "run.runFile", "Rerun": "run.rerun",
	"Debug": "run.debug", "DebugClass": "run.debugFile", "RerunFailedTests": "test.rerunFailed",
}

// keyNames maps key names of keystrokes ("meta shift OPEN_BRACKET") to
// Tiegel's chord key names.
var keyNames = map[string]string{
	"BACK_SPACE": keymap.KeyBackspace, "DELETE": keymap.KeyDelete, "ENTER": keymap.KeyEnter,
	"ESCAPE": keymap.KeyEscape, "TAB": keymap.KeyTab, "SPACE": keymap.KeySpace,
	"LEFT": keymap.KeyLeft, "RIGHT": keymap.KeyRight, "UP": keymap.KeyUp, "DOWN": keymap.KeyDown,
	"HOME": keymap.KeyHome, "END": keymap.KeyEnd, "PAGE_UP": keymap.KeyPageUp, "PAGE_DOWN": keymap.KeyPageDown,
	"OPEN_BRACKET": "[", "CLOSE_BRACKET": "]", "SLASH": "/", "BACK_SLASH": "\\",
	"MINUS": "-", "EQUALS": "=", "PLUS": "=", "COMMA": ",", "PERIOD": ".", "SEMICOLON": ";",
	"QUOTE": "'", "BACK_QUOTE": "`", "SUBTRACT": "-", "ADD": "=", "MULTIPLY": "*", "DIVIDE": "/",
	"KP_LEFT": keymap.KeyLeft, "KP_RIGHT": keymap.KeyRight, "KP_UP": keymap.KeyUp, "KP_DOWN": keymap.KeyDown,
}

// ConvertKeystroke turns a keystroke of a keymap file ("meta alt L",
// "shift ctrl OPEN_BRACKET") into a Tiegel chord.
func ConvertKeystroke(s string) (keymap.Chord, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return keymap.Chord{}, fmt.Errorf("%w: empty keystroke", keymap.ErrBadChord)
	}
	var mods []string
	key := ""
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "shift":
			mods = append(mods, "Shift")
			continue
		case "ctrl", "control":
			mods = append(mods, "Ctrl")
			continue
		case "meta":
			mods = append(mods, "Cmd")
			continue
		case "alt", "altgraph":
			mods = append(mods, "Alt")
			continue
		case "pressed", "released", "typed":
			continue
		}
		if i != len(fields)-1 {
			return keymap.Chord{}, fmt.Errorf("%w: %q", keymap.ErrBadChord, s)
		}
		key = f
	}
	if key == "" {
		return keymap.Chord{}, fmt.Errorf("%w: %q has no key", keymap.ErrBadChord, s)
	}
	if n, ok := keyNames[strings.ToUpper(key)]; ok {
		key = n
	} else if rest, ok := strings.CutPrefix(strings.ToUpper(key), "NUMPAD"); ok && len(rest) == 1 {
		key = rest
	}
	c, err := keymap.ParseChord(strings.Join(append(mods, key), "+"))
	if err != nil {
		return keymap.Chord{}, err
	}
	return c, nil
}

// mouseButtons maps mouse shortcuts to Tiegel's pointer chords.
var mouseButtons = map[string]string{"button4": keymap.KeyMouseBack, "button5": keymap.KeyMouseForward}

// keymapFile is a parsed keymaps/*.xml.
type keymapFile struct {
	rel    string
	name   string
	parent string
	// actions maps reference action IDs to their entry (an action entry
	// replaces the parent's entry completely).
	actions map[string]*Element
	order   []string
}

// basePreset returns the Tiegel preset of a bundled keymap of the
// reference IDE ("" when there is no counterpart).
func basePreset(name string) keymap.Preset {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "mac os x"), strings.HasPrefix(n, "macos"):
		return keymap.PresetMac
	case n == "$default", strings.HasPrefix(n, "default for"):
		return keymap.PresetPC
	}
	return ""
}

func (s *appScan) keymap() {
	c, ok := s.comp("KeymapManager")
	active := ""
	if ok {
		active = c.el.Child("active_keymap").Attr("name")
	}
	if active == "" {
		return
	}
	files, _ := readXMLDir(s.src.Dir, "keymaps")
	byName := map[string]*keymapFile{}
	for _, f := range files {
		root := f.root
		if root.Name != "keymap" {
			continue
		}
		kf := &keymapFile{rel: f.rel, name: root.Attr("name"), parent: root.Attr("parent"), actions: map[string]*Element{}}
		for _, a := range root.All("action") {
			id := a.Attr("id")
			if _, dup := kf.actions[id]; !dup {
				kf.order = append(kf.order, id)
			}
			kf.actions[id] = a
		}
		byName[kf.name] = kf
	}
	for _, it := range mapKeymap(active, c.file, byName, s.opts.GOOS) {
		s.plan.add(it)
	}
}

// mapKeymap resolves the parent chain of keymap active and returns the
// keymap items: the preset and overrides, plus the actions and chords
// without a counterpart.
func mapKeymap(active, source string, byName map[string]*keymapFile, goos string) []Item {
	var chain []*keymapFile
	base := active
	seen := map[string]bool{}
	for {
		kf, ok := byName[base]
		if !ok || seen[base] {
			break
		}
		seen[base] = true
		chain = append(chain, kf)
		base = kf.parent
	}
	var items []Item
	preset := basePreset(base)
	desc := active
	if len(chain) > 0 {
		names := []string{}
		for _, kf := range chain {
			names = append(names, kf.name)
		}
		desc = strings.Join(append(names, base), " → ")
		source = chain[0].rel
	}
	it := Item{Area: AreaKeymap, Name: "Keymap", Source: source}
	if preset == "" {
		it.Status = StatusUnsupported
		it.Detail = fmt.Sprintf("%s: the base keymap %q has no counterpart (Tiegel has macOS and Windows/Linux presets)", desc, base)
		if len(chain) == 0 {
			return []Item{it}
		}
		preset = keymap.PresetFor(goos)
		it.Detail += "; changed shortcuts are applied to the " + string(preset) + " preset"
	}
	// Effective changed actions: the nearest keymap of the chain wins.
	changed := map[string]*Element{}
	var order []string
	for _, kf := range chain {
		for _, id := range kf.order {
			if _, ok := changed[id]; !ok {
				changed[id] = kf.actions[id]
				order = append(order, id)
			}
		}
	}
	defaults := map[keymap.Action][]keymap.Chord{}
	for _, d := range keymap.Defs() {
		var cs []keymap.Chord
		ch := d.Mac
		if preset == keymap.PresetPC {
			ch = d.PC
		}
		for _, s := range ch {
			if c, err := keymap.ParseChord(s); err == nil {
				cs = append(cs, c)
			}
		}
		defaults[d.ID] = cs
	}
	// Sources per Tiegel action.
	sources := map[keymap.Action][]string{}
	for ref, a := range ActionMap {
		sources[a] = append(sources[a], ref)
	}
	overrides := keymap.Overrides{}
	var unmapped, badChords []string
	for _, id := range order {
		a, ok := ActionMap[id]
		if !ok {
			unmapped = append(unmapped, id)
			continue
		}
		if _, done := overrides[a]; done {
			continue
		}
		var chords []keymap.Chord
		all := true
		for _, ref := range sources[a] {
			el, isChanged := changed[ref]
			if !isChanged {
				all = false
				continue
			}
			for _, ks := range el.All("keyboard-shortcut") {
				if ks.Attr("second-keystroke") != "" {
					badChords = append(badChords, ref+": "+ks.Attr("first-keystroke")+", "+ks.Attr("second-keystroke")+" (two-stroke shortcuts are not supported)")
					continue
				}
				c, err := ConvertKeystroke(ks.Attr("first-keystroke"))
				if err != nil {
					badChords = append(badChords, ref+": "+ks.Attr("first-keystroke"))
					continue
				}
				chords = appendChord(chords, c)
			}
			for _, ms := range el.All("mouse-shortcut") {
				if k, ok := mouseButtons[strings.ToLower(strings.TrimSpace(ms.Attr("keystroke")))]; ok {
					chords = appendChord(chords, keymap.Chord{Key: k})
				} else {
					badChords = append(badChords, ref+": mouse "+ms.Attr("keystroke"))
				}
			}
		}
		if !all {
			// Another reference action of the same Tiegel action keeps its
			// default chords.
			for _, c := range defaults[a] {
				chords = appendChord(chords, c)
			}
		}
		if sameChords(chords, defaults[a]) {
			continue
		}
		if chords == nil {
			chords = []keymap.Chord{}
		}
		overrides[a] = chords
	}
	it.Changes = []Change{Setting(keymap.SettingPreset, ScopeGlobal, string(preset))}
	if lines := overrides.Format(); len(lines) > 0 {
		it.Changes = append(it.Changes, Change{Key: keymap.SettingOverrides, Scope: ScopeGlobal, Value: lines, Merge: MergeKeymap})
	}
	if it.Status == "" {
		it.Detail = fmt.Sprintf("%s: preset %s, %d changed shortcuts", desc, preset, len(overrides))
	}
	items = append(items, it)
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		items = append(items, Item{Area: AreaKeymap, Name: "Shortcuts without counterpart", Source: source, Status: StatusUnsupported,
			Detail: fmt.Sprintf("%d actions: %s", len(unmapped), strings.Join(unmapped, ", "))})
	}
	if len(badChords) > 0 {
		items = append(items, Item{Area: AreaKeymap, Name: "Unsupported shortcuts", Source: source, Status: StatusUnsupported,
			Detail: strings.Join(badChords, "; ")})
	}
	return items
}

func appendChord(list []keymap.Chord, c keymap.Chord) []keymap.Chord {
	if slices.Contains(list, c) {
		return list
	}
	return append(list, c)
}

func sameChords(a, b []keymap.Chord) bool {
	if len(a) != len(b) {
		return false
	}
	for _, c := range a {
		if !slices.Contains(b, c) {
			return false
		}
	}
	return true
}
