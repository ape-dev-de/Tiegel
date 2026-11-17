package controller

import (
	"context"
	"time"
)

// ProbeInfo is the info part of an interp.probe result.
type ProbeInfo struct {
	Version        string   `json:"version"`
	SAPI           string   `json:"sapi"`
	OS             string   `json:"os"`
	Extensions     []string `json:"extensions"`
	ZendExtensions []string `json:"zendExtensions"`
	Xdebug         string   `json:"xdebug"`
	IniFile        string   `json:"iniFile"`
	IniScanned     []string `json:"iniScanned"`
}

// ProbeResult is the result of interp.probe.
type ProbeResult struct {
	ID       string    `json:"id"`
	Info     ProbeInfo `json:"info"`
	Cached   bool      `json:"cached"`
	ProbedAt time.Time `json:"probedAt"`
}

// ProbeState is the interpreter info shown on the PHP interpreter page.
type ProbeState struct {
	// ID is the interpreter the state belongs to.
	ID     string
	Result *ProbeResult
	Err    string
	Busy   bool
}

// loadProbe shows the cached info of the selected interpreter; it never
// runs PHP (cachedOnly), so opening the page starts no container.
func (c *Controller) loadProbe() {
	id := ""
	for _, i := range c.Interpreters {
		if i.Selected {
			id = i.ID
		}
	}
	if id == "" {
		c.Probe = ProbeState{}
		return
	}
	c.Go(func(ctx context.Context) func() {
		var res ProbeResult
		err := c.call(ctx, "interp.probe", map[string]any{"id": id, "cachedOnly": true}, &res)
		return func() {
			c.Probe = ProbeState{ID: id}
			if err == nil {
				c.Probe.Result = &res
			}
		}
	})
}

// RefreshProbe re-reads the info of interpreter id ("Re-read" button).
// It is an explicit user action, so a stopped compose service may be
// started (run --rm) and an image may be pulled.
func (c *Controller) RefreshProbe(id string) {
	c.Probe = ProbeState{ID: id, Result: c.Probe.Result, Busy: true}
	c.Go(func(ctx context.Context) func() {
		var res ProbeResult
		err := c.call(ctx, "interp.probe", map[string]any{"id": id, "refresh": true, "start": true}, &res)
		return func() {
			if c.Probe.ID != id {
				return
			}
			c.Probe.Busy = false
			if err != nil {
				c.Probe.Err = err.Error()
				return
			}
			c.Probe.Result, c.Probe.Err = &res, ""
		}
	})
}
