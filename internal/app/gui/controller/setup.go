package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Setting candidates, discovered interpreters and the project setup
// (D521, D524, D526). Everything is loaded in the background; the
// discovery reads local files only.

// Candidate is a discovered value of a setting (settings.candidates).
type Candidate struct {
	Value       any    `json:"value"`
	Label       string `json:"label"`
	Detail      string `json:"detail"`
	Recommended bool   `json:"recommended"`
	Current     bool   `json:"current"`
}

// CandidateCache holds the candidates per setting key of one project.
type CandidateCache struct {
	root    string
	loading map[string]bool
	items   map[string][]Candidate
}

// Candidates returns the loaded candidates of key and whether they are
// being looked up.
func (c *Controller) Candidates(key string) ([]Candidate, bool) {
	cc := &c.Cands
	return cc.items[key], cc.loading[key]
}

// EnsureCandidates loads the candidates of key once per project.
func (c *Controller) EnsureCandidates(key string) {
	cc := &c.Cands
	if cc.root != c.Project.Root || cc.items == nil {
		cc.root, cc.items, cc.loading = c.Project.Root, map[string][]Candidate{}, map[string]bool{}
	}
	if _, ok := cc.items[key]; ok || cc.loading[key] {
		return
	}
	c.RefreshCandidates(key)
}

// RefreshCandidates looks the candidates of key up again.
func (c *Controller) RefreshCandidates(key string) {
	cc := &c.Cands
	if cc.items == nil {
		cc.root, cc.items, cc.loading = c.Project.Root, map[string][]Candidate{}, map[string]bool{}
	}
	cc.loading[key] = true
	root := c.Project.Root
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Candidates []Candidate `json:"candidates"`
		}
		err := c.call(ctx, "settings.candidates", map[string]string{"key": key}, &res)
		return func() {
			if cc.root != root {
				return
			}
			delete(cc.loading, key)
			if err != nil {
				cc.items[key] = []Candidate{}
				return
			}
			if res.Candidates == nil {
				res.Candidates = []Candidate{}
			}
			cc.items[key] = res.Candidates
		}
	})
}

// DiscoveredInterpreter is an interpreter found around the project that is
// not configured (interp.list discover=true).
type DiscoveredInterpreter struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Kind     string    `json:"kind"`
	Service  string    `json:"service"`
	Score    int       `json:"score"`
	Image    string    `json:"image"`
	Remote   string    `json:"remote"`
	Files    []string  `json:"files"`
	Dir      string    `json:"dir"`
	Relation string    `json:"relation"`
	Reasons  []string  `json:"reasons"`
	Mappings []Mapping `json:"mappings"`
	// Discovered is false for configured interpreters (filtered out).
	Discovered bool `json:"discovered"`
}

// DiscoveredState is the list on the Interpreter page.
type DiscoveredState struct {
	Loading bool
	Items   []DiscoveredInterpreter
}

// LoadDiscovered lists the interpreters found around the project.
func (c *Controller) LoadDiscovered() {
	if !c.Project.Open {
		c.Discovered = DiscoveredState{}
		return
	}
	c.Discovered.Loading = true
	root := c.Project.Root
	c.Go(func(ctx context.Context) func() {
		var list []DiscoveredInterpreter
		err := c.call(ctx, "interp.list", map[string]bool{"discover": true}, &list)
		return func() {
			if c.Project.Root != root {
				return
			}
			c.Discovered = DiscoveredState{}
			if err != nil {
				return
			}
			for _, it := range list {
				if it.Discovered {
					c.Discovered.Items = append(c.Discovered.Items, it)
				}
			}
		}
	})
}

// UseDiscovered configures and selects a discovered interpreter: a
// compose service through interp.select (which stores its compose files),
// an image through interp.addImage first.
func (c *Controller) UseDiscovered(id string) {
	var it *DiscoveredInterpreter
	for i := range c.Discovered.Items {
		if c.Discovered.Items[i].ID == id {
			it = &c.Discovered.Items[i]
		}
	}
	if it == nil {
		return
	}
	d := *it
	c.Go(func(ctx context.Context) func() {
		var err error
		if d.Kind == "docker" {
			err = c.call(ctx, "interp.addImage", map[string]string{"image": d.Image, "remote": d.Remote}, nil)
		}
		if err == nil {
			err = c.call(ctx, "interp.select", map[string]string{"id": d.ID}, nil)
		}
		return func() {
			c.notice(err)
			if err == nil {
				c.Status.Notice = "Interpreter: " + d.Label
			}
			c.Cands = CandidateCache{}
			c.LoadSettingsData()
			c.LoadDiscovered()
			c.refreshRuntime()
		}
	})
}

// SetupItem is a proposed setting of project.setupProposal.
type SetupItem struct {
	Key      string      `json:"key"`
	Label    string      `json:"label"`
	Owner    string      `json:"owner"`
	Proposed Candidate   `json:"proposed"`
	Choices  []Candidate `json:"choices"`
}

// SetupState is the project setup sheet.
type SetupState struct {
	// Offer: the opened project has proposals and the sheet was not
	// dismissed; the view shows it once.
	Offer bool
	// Open, Busy: the sheet is shown / applying.
	Open, Busy bool
	Items      []SetupItem
	Dismissed  bool
	Message    string
	Error      bool
	// Version changes when Items change (the view rebuilds its sheet).
	Version int
}

type setupProposal struct {
	Dismissed bool        `json:"dismissed"`
	Notable   bool        `json:"notable"`
	Items     []SetupItem `json:"items"`
}

// checkProjectSetup loads the proposal of a newly opened project and
// offers the sheet when something is proposed and it was not dismissed.
func (c *Controller) checkProjectSetup() {
	root := c.Project.Root
	c.Setup = SetupState{}
	c.Cands = CandidateCache{}
	c.Discovered = DiscoveredState{}
	c.Go(func(ctx context.Context) func() {
		var p setupProposal
		if err := c.call(ctx, "project.setupProposal", nil, &p); err != nil {
			return nil
		}
		return func() {
			if c.Project.Root != root {
				return
			}
			c.Setup.Items, c.Setup.Dismissed = p.Items, p.Dismissed
			c.Setup.Version++
			c.Setup.Offer = p.Notable && !p.Dismissed
		}
	})
}

// OpenProjectSetup loads the proposal and shows the sheet (also when it
// was dismissed).
func (c *Controller) OpenProjectSetup() {
	c.Setup.Open, c.Setup.Offer, c.Setup.Busy, c.Setup.Message, c.Setup.Error = true, false, true, "Looking for the project's tools…", false
	root := c.Project.Root
	c.Go(func(ctx context.Context) func() {
		var p setupProposal
		err := c.call(ctx, "project.setupProposal", nil, &p)
		return func() {
			if c.Project.Root != root {
				return
			}
			c.Setup.Busy, c.Setup.Message = false, ""
			if err != nil {
				c.Setup.Message, c.Setup.Error = err.Error(), true
				return
			}
			c.Setup.Items, c.Setup.Dismissed = p.Items, p.Dismissed
			c.Setup.Version++
		}
	})
}

// CloseProjectSetup closes the sheet (asked again on the next open).
func (c *Controller) CloseProjectSetup() {
	c.Setup.Open, c.Setup.Offer = false, false
}

// SetupValue is a chosen value of the sheet.
type SetupValue struct {
	Key   string
	Value any
}

// ApplySetup writes the chosen values (project.setupApply).
func (c *Controller) ApplySetup(values []SetupValue) {
	items := make([]map[string]any, 0, len(values))
	for _, v := range values {
		raw, err := json.Marshal(v.Value)
		if err != nil {
			c.notice(err)
			return
		}
		items = append(items, map[string]any{"key": v.Key, "value": json.RawMessage(raw)})
	}
	c.Setup.Busy, c.Setup.Message, c.Setup.Error = true, "Applying…", false
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Applied []string `json:"applied"`
			Errors  []string `json:"errors"`
		}
		err := c.call(ctx, "project.setupApply", map[string]any{"items": items}, &res)
		return func() {
			c.Setup.Busy = false
			switch {
			case err != nil:
				c.Setup.Message, c.Setup.Error = err.Error(), true
			case len(res.Errors) > 0:
				c.Setup.Message, c.Setup.Error = strings.Join(res.Errors, "; "), true
			default:
				c.Setup.Open = false
				c.Status.Notice = fmt.Sprintf("Project setup: %d setting(s) applied", len(res.Applied))
			}
			c.Cands = CandidateCache{}
			c.LoadSettingsData()
			c.refreshRuntime()
		}
	})
}

// DismissSetup closes the sheet and does not offer it again for the
// project (Project Setup… still opens it).
func (c *Controller) DismissSetup() {
	c.Setup.Open, c.Setup.Offer, c.Setup.Dismissed = false, false, true
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "project.setupApply", map[string]any{"dismiss": true}, nil)
		return func() { c.notice(err) }
	})
}
