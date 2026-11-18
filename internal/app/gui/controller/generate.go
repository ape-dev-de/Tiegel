package controller

import (
	"context"
	"strings"
)

// Generate (Cmd+N, plugin php-generate, AP17.3): the backend lists the
// language server's generating code actions and its own generators
// (constructor, getters, setters); the view offers them in a chooser.

// GenerateAction is an entry of the Generate menu (generate.list).
type GenerateAction struct {
	Source     string   `json:"source"` // "builtin" or "lsp"
	Kind       string   `json:"kind"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Properties []string `json:"properties"`
	Disabled   string   `json:"disabled"`
}

// GenerateMenu is the result of generate.list at a position.
type GenerateMenu struct {
	File    string
	Offset  int
	Class   string           `json:"class"`
	Actions []GenerateAction `json:"actions"`
}

// LoadGenerate asks what can be generated at offset off of file and
// hands the menu to done on the UI goroutine.
func (c *Controller) LoadGenerate(file string, off int, done func(GenerateMenu)) {
	c.Go(func(ctx context.Context) func() {
		var m GenerateMenu
		err := c.call(ctx, "generate.list", map[string]any{"path": file, "pos": pos{Offset: off}, "noLsp": !c.Status.LSPRunning}, &m)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			m.File, m.Offset = file, off
			done(m)
		}
	})
}

// RunGenerate runs action a of menu m; props limits a built-in generator
// (nil: all its candidates).
func (c *Controller) RunGenerate(m GenerateMenu, a GenerateAction, props []string) {
	if a.Disabled != "" {
		c.Status.Notice = a.Title + ": " + a.Disabled
		return
	}
	c.Go(func(ctx context.Context) func() {
		if a.Source == "lsp" {
			err := c.call(ctx, "lsp.applyCodeAction", map[string]string{"id": a.ID}, nil)
			return func() {
				if err != nil {
					c.notice(err)
					return
				}
				c.Status.Notice = "Applied: " + a.Title
			}
		}
		p := map[string]any{"path": m.File, "pos": pos{Offset: m.Offset}, "kind": a.Kind}
		if len(props) > 0 {
			p["properties"] = props
		}
		var res struct {
			Methods []string `json:"methods"`
			Caret   int      `json:"caret"`
		}
		err := c.call(ctx, "generate.apply", p, &res)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Generated " + strings.Join(res.Methods, ", ")
			loc := At(m.File, res.Caret)
			c.Reveal = &loc
		}
	})
}
