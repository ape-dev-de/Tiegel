package atoms

import (
	"image"
	"image/color"
	"sync"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Glyph is vector icon data (IconVG). Glyphs are immutable and safe to share;
// rasterizations are cached per size and color.
type Glyph struct {
	name string
	data []byte

	mu    sync.Mutex
	valid bool
	init  bool
	cache map[glyphKey]*widget.Icon
}

type glyphKey struct {
	size int
	col  color.NRGBA
}

// maxGlyphCache bounds the number of cached rasterizations per glyph.
const maxGlyphCache = 32

// NewGlyph wraps IconVG data. Invalid data yields a glyph that draws nothing.
func NewGlyph(name string, ivg []byte) *Glyph {
	return &Glyph{name: name, data: ivg}
}

// Name returns the glyph's identifier.
func (g *Glyph) Name() string {
	if g == nil {
		return ""
	}
	return g.name
}

// Valid reports whether the glyph data decodes.
func (g *Glyph) Valid() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.load()
	return g.valid
}

func (g *Glyph) load() {
	if g.init {
		return
	}
	g.init = true
	_, err := widget.NewIcon(g.data)
	g.valid = err == nil
	g.cache = make(map[glyphKey]*widget.Icon)
}

func (g *Glyph) icon(size int, col color.NRGBA) *widget.Icon {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.load()
	if !g.valid {
		return nil
	}
	k := glyphKey{size, col}
	if ic, ok := g.cache[k]; ok {
		return ic
	}
	if len(g.cache) >= maxGlyphCache {
		clear(g.cache)
	}
	ic, err := widget.NewIcon(g.data)
	if err != nil {
		return nil
	}
	g.cache[k] = ic
	return ic
}

// Built-in glyphs. They come from the Material Design icon set
// (Apache 2.0, via golang.org/x/exp/shiny/materialdesign/icons).
var (
	GlyphFile          = NewGlyph("file", icons.EditorInsertDriveFile)
	GlyphFileCode      = NewGlyph("file-code", icons.ActionCode)
	GlyphFileText      = NewGlyph("file-text", icons.ActionDescription)
	GlyphFolder        = NewGlyph("folder", icons.FileFolder)
	GlyphFolderOpen    = NewGlyph("folder-open", icons.FileFolderOpen)
	GlyphChevronRight  = NewGlyph("chevron-right", icons.NavigationChevronRight)
	GlyphChevronDown   = NewGlyph("chevron-down", icons.NavigationExpandMore)
	GlyphClose         = NewGlyph("close", icons.NavigationClose)
	GlyphCheck         = NewGlyph("check", icons.NavigationCheck)
	GlyphSearch        = NewGlyph("search", icons.ActionSearch)
	GlyphAdd           = NewGlyph("add", icons.ContentAdd)
	GlyphRemove        = NewGlyph("remove", icons.ContentRemove)
	GlyphSettings      = NewGlyph("settings", icons.ActionSettings)
	GlyphRun           = NewGlyph("run", icons.AVPlayArrow)
	GlyphDebug         = NewGlyph("debug", icons.ActionBugReport)
	GlyphRefresh       = NewGlyph("refresh", icons.NavigationRefresh)
	GlyphHistory       = NewGlyph("history", icons.ActionHistory)
	GlyphBranch        = NewGlyph("branch", icons.CommunicationCallSplit)
	GlyphInfo          = NewGlyph("info", icons.ActionInfo)
	GlyphWarning       = NewGlyph("warning", icons.AlertWarning)
	GlyphError         = NewGlyph("error", icons.AlertError)
	GlyphDone          = NewGlyph("done", icons.ActionDone)
	GlyphThemeDark     = NewGlyph("theme-dark", icons.ImageBrightness2)
	GlyphThemeLight    = NewGlyph("theme-light", icons.ImageWBSunny)
	GlyphIndeterminate = NewGlyph("indeterminate", icons.ToggleIndeterminateCheckBox)
	GlyphStop          = NewGlyph("stop", icons.AVStop)
	GlyphPause         = NewGlyph("pause", icons.AVPause)
	GlyphStepOver      = NewGlyph("step-over", icons.ContentRedo)
	GlyphStepInto      = NewGlyph("step-into", icons.NavigationArrowDownward)
	GlyphStepOut       = NewGlyph("step-out", icons.NavigationArrowUpward)
	GlyphMute          = NewGlyph("mute", icons.ContentBlock)
	GlyphListen        = NewGlyph("listen", icons.ActionSettingsInputAntenna)
	GlyphCopy          = NewGlyph("copy", icons.ContentContentCopy)
	GlyphEdit          = NewGlyph("edit", icons.EditorModeEdit)
	GlyphComputer      = NewGlyph("computer", icons.HardwareComputer)
	GlyphContainer     = NewGlyph("container", icons.ActionDNS)
	GlyphMore          = NewGlyph("more", icons.NavigationMoreHoriz)
	GlyphDropDown      = NewGlyph("drop-down", icons.NavigationArrowDropDown)
	GlyphList          = NewGlyph("list", icons.ActionList)
	GlyphSwap          = NewGlyph("swap", icons.ActionSwapHoriz)
	GlyphChevronUp     = NewGlyph("chevron-up", icons.NavigationExpandLess)
	GlyphArrowLeft     = NewGlyph("arrow-left", icons.NavigationArrowBack)
	GlyphArrowRight    = NewGlyph("arrow-right", icons.NavigationArrowForward)
	GlyphUpload        = NewGlyph("upload", icons.FileFileUpload)
	GlyphDelete        = NewGlyph("delete", icons.ActionDelete)
	GlyphCommit        = NewGlyph("commit", icons.ActionCheckCircle)
	GlyphCloud         = NewGlyph("cloud", icons.FileCloud)
	GlyphTag           = NewGlyph("tag", icons.ActionLabel)
)

// Glyphs lists all built-in glyphs (for the catalog and tests).
var Glyphs = []*Glyph{
	GlyphFile, GlyphFileCode, GlyphFileText, GlyphFolder, GlyphFolderOpen,
	GlyphChevronRight, GlyphChevronDown, GlyphClose, GlyphCheck, GlyphSearch,
	GlyphAdd, GlyphRemove, GlyphSettings, GlyphRun, GlyphDebug, GlyphRefresh,
	GlyphHistory, GlyphBranch, GlyphInfo, GlyphWarning, GlyphError, GlyphDone,
	GlyphThemeDark, GlyphThemeLight, GlyphIndeterminate, GlyphChevronUp,
	GlyphArrowLeft, GlyphArrowRight, GlyphUpload, GlyphEdit, GlyphDelete,
	GlyphCommit, GlyphCloud, GlyphTag,
	GlyphStop, GlyphPause, GlyphStepOver, GlyphStepInto, GlyphStepOut, GlyphMute,
	GlyphListen, GlyphCopy, GlyphComputer, GlyphContainer, GlyphMore,
	GlyphDropDown, GlyphList, GlyphSwap,
}

// GlyphNamed returns the built-in glyph with the given name (see
// Glyph.Name, e.g. "delete"), or nil. Gio-free models such as
// internal/actions refer to icons by name.
func GlyphNamed(name string) *Glyph {
	if name == "" {
		return nil
	}
	for _, g := range Glyphs {
		if g.name == name {
			return g
		}
	}
	return nil
}

// Icon draws a glyph in a square box. It is a value component.
type Icon struct {
	Glyph *Glyph
	// Size defaults to th.Size.Icon.
	Size unit.Dp
	// Color defaults to th.Color.Text when its alpha is zero.
	Color color.NRGBA
}

// NewIcon returns an icon with default size and color.
func NewIcon(g *Glyph) Icon { return Icon{Glyph: g} }

// Fn adapts the icon to a layout.Widget.
func (i Icon) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return i.Layout(gtx, th) }
}

// Layout draws the icon. Without an explicit Color it uses th.Color.Text,
// or th.Color.TextDisabled in a disabled context. A nil or invalid glyph reserves the space only.
func (i Icon) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := i.Size
	if size == 0 {
		size = th.Size.Icon
	}
	col := i.Color
	if col.A == 0 {
		col = th.Color.Text
		if !gtx.Enabled() {
			col = th.Color.TextDisabled
		}
	}
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	if ic := i.Glyph.iconOrNil(px, col); ic != nil {
		cgtx := gtx
		cgtx.Constraints = layout.Exact(sz)
		ic.Layout(cgtx, col)
	}
	return layout.Dimensions{Size: sz}
}

func (g *Glyph) iconOrNil(px int, col color.NRGBA) *widget.Icon {
	if g == nil || px <= 0 {
		return nil
	}
	return g.icon(px, col)
}
