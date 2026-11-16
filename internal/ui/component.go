package ui

import (
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Component is the common interface of all Tiegel UI components.
type Component interface {
	Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions
}

// ComponentFunc adapts a function to Component.
type ComponentFunc func(gtx layout.Context, th *tokens.Theme) layout.Dimensions

// Layout calls f.
func (f ComponentFunc) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return f(gtx, th)
}

// Widget binds c to th, yielding a layout.Widget for Gio layouts.
func Widget(c Component, th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return c.Layout(gtx, th) }
}
