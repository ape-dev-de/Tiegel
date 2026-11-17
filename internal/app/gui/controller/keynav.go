package controller

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// ---- keymap preferences (M13) ---------------------------------------------------

// KeymapPrefs are the stored keymap settings (keymap.preset,
// keymap.overrides). Version changes whenever they were (re)loaded with a
// different value, so views rebuild their bindings live.
type KeymapPrefs struct {
	Preset    string
	Overrides []string
	Version   uint64
}

// LoadKeymap (re)reads the keymap settings; the view resolves them with
// keymap.NewScheme.
func (c *Controller) LoadKeymap() {
	c.Go(func(ctx context.Context) func() {
		var p, o settingValue
		perr := c.call(ctx, "settings.get", map[string]string{"key": keymap.SettingPreset}, &p)
		oerr := c.call(ctx, "settings.get", map[string]string{"key": keymap.SettingOverrides}, &o)
		var preset string
		var lines []string
		if perr == nil && p.Found {
			_ = json.Unmarshal(p.Value, &preset)
		}
		if oerr == nil && o.Found {
			_ = json.Unmarshal(o.Value, &lines)
		}
		apply := c.fetchKeymapList(ctx)
		return func() {
			apply()
			if preset == c.Keymap.Preset && slices.Equal(lines, c.Keymap.Overrides) && c.Keymap.Version > 0 {
				return
			}
			c.Keymap = KeymapPrefs{Preset: preset, Overrides: lines, Version: c.Keymap.Version + 1}
		}
	})
}

// settingsChange is the payload of settings.changed.
type settingsChange struct {
	Key string `json:"Key"`
}

// keymapSettingChanged reports whether a settings.changed payload
// concerns the keymap.
func keymapSettingChanged(payload json.RawMessage) bool {
	var ch settingsChange
	return json.Unmarshal(payload, &ch) == nil && strings.HasPrefix(ch.Key, "keymap.")
}

// ---- recent locations and edit locations ----------------------------------------

// Place is an entry of Recent Locations: where the caret was (left by a
// jump) or where the user edited.
type Place struct {
	Location
	// Line is the 1-based line (0: unknown, e.g. an LSP location before
	// the file was shown).
	Line int
	// Snippet is the trimmed text of the line.
	Snippet string
	// Edit marks edit locations (Last Edit Location, "changed" filter).
	Edit bool
}

// maxPlaces bounds Recent Locations.
const maxPlaces = 50

// Record adds p as the newest place. A place on the same file and line
// replaces the older one (keeping its edit mark).
func (n *Navigation) Record(p Place) {
	for i, q := range n.places {
		if q.File == p.File && (q.Line == p.Line && p.Line > 0 || q.Range.Start.Offset == p.Range.Start.Offset) {
			p.Edit = p.Edit || q.Edit
			n.places = slices.Delete(n.places, i, i+1)
			break
		}
	}
	n.places = slices.Insert(n.places, 0, p)
	if len(n.places) > maxPlaces {
		n.places = n.places[:maxPlaces]
	}
	if p.Edit {
		n.editCursor = 0
	}
}

// Places returns the recent places, newest first.
func (n *Navigation) Places() []Place { return slices.Clone(n.places) }

// LastEdit returns the most recent edit place that is not the current
// line (cur, curLine); repeated calls without new edits go further back.
func (n *Navigation) LastEdit(cur Location, curLine int) (Place, bool) {
	k := 0
	for _, p := range n.places {
		if !p.Edit {
			continue
		}
		if k < n.editCursor {
			k++
			continue
		}
		k++
		n.editCursor = k
		if p.File == cur.File && p.Line == curLine && curLine > 0 {
			continue
		}
		return p, true
	}
	return Place{}, false
}

// ---- next / previous highlighted error -------------------------------------------

// NextDiagnostic returns the diagnostic after (forward) or before the
// caret offset, wrapping around at the document end.
func NextDiagnostic(ds []Diagnostic, caret int, forward bool) (Diagnostic, bool) {
	if len(ds) == 0 {
		return Diagnostic{}, false
	}
	sorted := slices.Clone(ds)
	slices.SortStableFunc(sorted, func(a, b Diagnostic) int { return a.Start - b.Start })
	if forward {
		for _, d := range sorted {
			if d.Start > caret {
				return d, true
			}
		}
		return sorted[0], true
	}
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i].Start < caret {
			return sorted[i], true
		}
	}
	return sorted[len(sorted)-1], true
}
