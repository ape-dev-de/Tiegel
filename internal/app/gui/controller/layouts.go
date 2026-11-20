package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Named tool window layouts (D476): stored globally through the kernel
// ("layout.*"), so that the same arrangement can be applied to every
// project and from the command line.

// layoutState mirrors api.LayoutState.
type layoutState struct {
	Name   string          `json:"name"`
	Found  bool            `json:"found"`
	Layout json.RawMessage `json:"layout"`
	Names  []string        `json:"names"`
}

// ResetToolLayout restores the default tool window layout of the
// project (persisted on the next frame).
func (c *Controller) ResetToolLayout() {
	c.Tools.Reset()
	c.Status.Notice = "Tool window layout reset"
}

// ToolLayoutNames loads the names of the saved layouts and passes them
// to done (on the UI goroutine).
func (c *Controller) ToolLayoutNames(done func(names []string)) {
	c.Go(func(ctx context.Context) func() {
		var st layoutState
		err := c.call(ctx, "layout.get", map[string]string{}, &st)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			done(st.Names)
		}
	})
}

// SaveToolLayout stores the current tool window layout under name.
func (c *Controller) SaveToolLayout(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	raw, err := json.Marshal(c.Tools)
	if err != nil {
		c.notice(fmt.Errorf("save layout: %w", err))
		return
	}
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "layout.save", map[string]any{"name": name, "layout": json.RawMessage(raw)}, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Layout “" + name + "” saved"
		}
	})
}

// ApplyToolLayout replaces the tool window layout by the saved layout
// name (persisted on the next frame).
func (c *Controller) ApplyToolLayout(name string) {
	tools := c.Tools
	c.Go(func(ctx context.Context) func() {
		var st layoutState
		err := c.call(ctx, "layout.get", map[string]string{"name": name}, &st)
		return func() {
			if err == nil && c.Tools == tools {
				err = tools.Apply(st.Layout)
			}
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Layout “" + name + "” applied"
		}
	})
}

// DeleteToolLayout removes the saved layout name.
func (c *Controller) DeleteToolLayout(name string) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "layout.delete", map[string]string{"name": name}, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Layout “" + name + "” deleted"
		}
	})
}
