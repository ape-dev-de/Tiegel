package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Run tool window state (AP15.1): one tab per run with its output, the
// run configurations of the project for the Run button, and the close
// confirmation. The backend is the kernel's run.* service.

// maxRunLines bounds the lines a run tab keeps (like the backend).
const maxRunLines = run.DefaultMaxLines

// RunTab is the output of one run.
type RunTab struct {
	Info  run.Info
	Lines []run.Line
	// Dropped tells that older lines were discarded.
	Dropped bool
	// Version changes with Info and Lines.
	Version uint64
	// closeOnExit removes the tab once the stopped run ended.
	closeOnExit bool
}

// Active reports whether the run has not finished.
func (t *RunTab) Active() bool { return t.Info.Active() }

// Runs is the run state of the window.
type Runs struct {
	c *Controller
	// Tabs are the run tabs in creation order; Selected is the shown one
	// (run ID, "" none).
	Tabs     []*RunTab
	Selected string
	// Configs are the project's run configurations, Types the
	// configuration types (LoadConfigs).
	Configs []api.RunConfigInfo
	Types   []api.RunConfigTypeInfo
	// Version changes whenever tabs, selection or configurations change.
	Version uint64
	// Started is set when a run started from this window (the view
	// shows the tool window); the view clears it.
	Started bool
	project string
	// chosen is the configuration the user picked last in this window; a
	// configuration list read before the selection reached the backend
	// must not undo it.
	chosen string
	// cfgSeq numbers LoadConfigs requests; only the latest answer is
	// applied (an older list may carry an outdated selection).
	cfgSeq uint64
}

func newRuns(c *Controller) *Runs { return &Runs{c: c} }

// Tab returns the tab of run id.
func (r *Runs) Tab(id string) *RunTab {
	for _, t := range r.Tabs {
		if t.Info.ID == id {
			return t
		}
	}
	return nil
}

// Current returns the selected tab (nil without tabs).
func (r *Runs) Current() *RunTab {
	if t := r.Tab(r.Selected); t != nil {
		return t
	}
	if len(r.Tabs) > 0 {
		return r.Tabs[len(r.Tabs)-1]
	}
	return nil
}

// ActiveCount returns the number of runs that have not finished.
func (r *Runs) ActiveCount() int {
	n := 0
	for _, t := range r.Tabs {
		if t.Active() {
			n++
		}
	}
	return n
}

// SelectedConfig returns the configuration the Run button starts.
func (r *Runs) SelectedConfig() *api.RunConfigInfo {
	for i := range r.Configs {
		if r.Configs[i].Selected {
			return &r.Configs[i]
		}
	}
	return nil
}

// Select shows the tab of run id.
func (r *Runs) Select(id string) {
	if r.Tab(id) != nil && r.Selected != id {
		r.Selected = id
		r.Version++
	}
}

// handleEvent follows run.* events (UI goroutine).
func (r *Runs) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicRunStarted:
		var info run.Info
		if json.Unmarshal(payload, &info) == nil && (info.Project == "" || info.Project == r.c.Project.Root) {
			r.upsert(info)
		}
	case api.TopicRunOutput:
		var o run.Output
		if json.Unmarshal(payload, &o) == nil {
			if t := r.Tab(o.RunID); t != nil {
				r.appendLines(t, o.Lines)
			}
		}
	case api.TopicRunExited:
		var info run.Info
		if json.Unmarshal(payload, &info) == nil {
			if t := r.Tab(info.ID); t != nil {
				t.Info = info
				t.Version++
				r.Version++
				if t.closeOnExit {
					r.CloseTab(info.ID)
				}
			}
		}
	case api.TopicRunRemoved:
		var p api.RunIDParams
		if json.Unmarshal(payload, &p) == nil {
			r.drop(p.RunID)
		}
	case api.TopicRunConfigsChanged:
		var list []api.RunConfigInfo
		if json.Unmarshal(payload, &list) == nil {
			r.Configs = list
			r.Version++
		}
	}
}

// upsert adds the tab of a started run; a rerun takes the tab of the
// run it replaces.
func (r *Runs) upsert(info run.Info) {
	if t := r.Tab(info.ID); t != nil {
		if !t.Info.Finished.IsZero() && info.Finished.IsZero() {
			// The start/rerun reply came after run.exited: keep the end.
			return
		}
		t.Info = info
		t.Version++
		r.Version++
		return
	}
	t := &RunTab{Info: info}
	if old := r.Tab(info.Replaces); info.Replaces != "" && old != nil {
		i := slices.Index(r.Tabs, old)
		r.Tabs[i] = t
	} else {
		r.Tabs = append(r.Tabs, t)
	}
	r.Selected = info.ID
	r.Version++
}

func (r *Runs) appendLines(t *RunTab, lines []run.Line) {
	for _, l := range lines {
		if n := len(t.Lines); n > 0 && l.Seq <= t.Lines[n-1].Seq {
			continue // already loaded (run.output page)
		}
		t.Lines = append(t.Lines, l)
	}
	if len(t.Lines) > maxRunLines {
		t.Lines = append(t.Lines[:0:0], t.Lines[len(t.Lines)-maxRunLines:]...)
		t.Dropped = true
	}
	t.Version++
	r.Version++
}

// drop removes a tab without asking the backend.
func (r *Runs) drop(id string) {
	i := slices.IndexFunc(r.Tabs, func(t *RunTab) bool { return t.Info.ID == id })
	if i < 0 {
		return
	}
	r.Tabs = slices.Delete(r.Tabs, i, i+1)
	if r.Selected == id {
		r.Selected = ""
		if len(r.Tabs) > 0 {
			r.Selected = r.Tabs[min(i, len(r.Tabs)-1)].Info.ID
		}
	}
	r.Version++
}

// ProjectChanged reloads configurations and runs for the open project
// (called by the view when the project root changes).
func (r *Runs) ProjectChanged() {
	root := r.c.Project.Root
	if root == r.project {
		return
	}
	r.project = root
	r.Configs, r.Types = nil, nil
	r.chosen = ""
	// Finished runs of another project go; active ones stay visible.
	r.Tabs = slices.DeleteFunc(r.Tabs, func(t *RunTab) bool { return !t.Active() && t.Info.Project != root })
	r.Version++
	if root == "" {
		return
	}
	r.LoadConfigs()
	r.c.Go(func(ctx context.Context) func() {
		var list []run.Info
		err := r.c.call(ctx, "run.list", api.RunListParams{}, &list)
		pages := map[string]run.Page{}
		for _, i := range list {
			if i.Project != root {
				continue
			}
			var pg run.Page
			if r.c.call(ctx, "run.output", api.RunOutputParams{RunID: i.ID}, &pg) == nil {
				pages[i.ID] = pg
			}
		}
		return func() {
			if err != nil {
				return
			}
			for _, i := range list {
				if i.Project != root || r.Tab(i.ID) != nil {
					continue
				}
				t := &RunTab{Info: i}
				r.Tabs = append(r.Tabs, t)
				pg := pages[i.ID]
				r.appendLines(t, pg.Lines)
				t.Dropped = t.Dropped || pg.Dropped
			}
			r.Version++
		}
	})
}

// LoadConfigs reads the configurations and their types.
func (r *Runs) LoadConfigs() {
	r.cfgSeq++
	seq := r.cfgSeq
	r.c.Go(func(ctx context.Context) func() {
		var list []api.RunConfigInfo
		err := r.c.call(ctx, "run.configs.list", nil, &list)
		var types []api.RunConfigTypeInfo
		terr := r.c.call(ctx, "run.configTypes", nil, &types)
		return func() {
			if seq != r.cfgSeq {
				return
			}
			if err == nil {
				r.Configs = list
				r.applyChosen()
			}
			if terr == nil {
				r.Types = types
			}
			r.Version++
		}
	})
}

// start calls run.start and shows the new run.
func (r *Runs) start(p api.RunStartParams) {
	r.c.Go(func(ctx context.Context) func() {
		var info run.Info
		err := r.c.call(ctx, "run.start", p, &info)
		return func() {
			if err != nil {
				r.c.Status.Notice = "Run: " + err.Error()
				return
			}
			r.upsert(info)
			r.Started = true
			if p.Config != "" {
				// run.start selected it; replace lists loaded before.
				r.LoadConfigs()
				// The backend selected the configuration; a list loaded
				// concurrently (LoadConfigs) may predate that.
				r.markSelected(info.Config)
			}
		}
	})
}

// markSelected marks configuration id as the selected one.
func (r *Runs) markSelected(id string) {
	if id == "" {
		return
	}
	for i := range r.Configs {
		r.Configs[i].Selected = r.Configs[i].ID == id
	}
	r.Version++
}

// RunSelected starts the selected configuration (Ctrl+R); without one
// the PHP file file runs (if given).
func (r *Runs) RunSelected(file string) {
	if cfg := r.SelectedConfig(); cfg != nil {
		r.RunConfig(cfg.ID)
		return
	}
	if file != "" {
		r.RunFile(file)
		return
	}
	r.c.Status.Notice = "No run configuration – add one in the Run tool window or run the current file"
}

// RunConfig starts configuration id and makes it the one Ctrl+R runs.
func (r *Runs) RunConfig(id string) {
	r.start(api.RunStartParams{Config: id})
	r.SelectConfig(id)
}

// applyChosen marks the configuration picked in this window as selected
// if the list contains it.
func (r *Runs) applyChosen() {
	if r.chosen == "" || !slices.ContainsFunc(r.Configs, func(c api.RunConfigInfo) bool { return c.ID == r.chosen }) {
		return
	}
	for i := range r.Configs {
		r.Configs[i].Selected = r.Configs[i].ID == r.chosen
	}
}

// DebugSelected starts the selected configuration under the debugger
// (Ctrl+D); without one the PHP file file is debugged (if given).
func (r *Runs) DebugSelected(file string) {
	if cfg := r.SelectedConfig(); cfg != nil {
		r.start(api.RunStartParams{Config: cfg.ID, Debug: true})
		return
	}
	if file != "" {
		r.DebugFile(file)
		return
	}
	r.c.Status.Notice = "No run configuration – add one in the Run tool window or debug the current file"
}

// DebugFile runs file as PHP script under the debugger.
func (r *Runs) DebugFile(file string) {
	if file == "" {
		r.c.Status.Notice = "Debug: no file"
		return
	}
	r.start(api.RunStartParams{File: file, Debug: true})
}

// RunFile runs file as PHP script with the project interpreter.
func (r *Runs) RunFile(file string) {
	if file == "" {
		r.c.Status.Notice = "Run: no file"
		return
	}
	r.start(api.RunStartParams{File: file})
}

// SelectConfig makes configuration id the one Ctrl+R runs.
func (r *Runs) SelectConfig(id string) {
	r.chosen = id
	r.applyChosen()
	r.Version++
	r.c.Go(func(ctx context.Context) func() {
		err := r.c.call(ctx, "run.configs.select", api.RunConfigIDParams{ID: id}, nil)
		return func() { r.c.notice(err) }
	})
}

// SaveConfig creates or updates a configuration; done (optional) gets
// the stored one.
func (r *Runs) SaveConfig(cfg run.Config, done func(run.Config)) {
	r.c.Go(func(ctx context.Context) func() {
		var saved run.Config
		err := r.c.call(ctx, "run.configs.save", cfg, &saved)
		return func() {
			if err != nil {
				r.c.Status.Notice = "Run configuration: " + err.Error()
				return
			}
			r.LoadConfigs()
			if done != nil {
				done(saved)
			}
		}
	})
}

// DeleteConfig deletes configuration id.
func (r *Runs) DeleteConfig(id string) {
	r.c.Go(func(ctx context.Context) func() {
		err := r.c.call(ctx, "run.configs.delete", api.RunConfigIDParams{ID: id}, nil)
		return func() {
			r.c.notice(err)
			r.LoadConfigs()
		}
	})
}

// Stop stops run id.
func (r *Runs) Stop(id string) {
	r.c.Go(func(ctx context.Context) func() {
		err := r.c.call(ctx, "run.stop", api.RunIDParams{RunID: id}, nil)
		return func() { r.c.notice(err) }
	})
}

// Rerun runs the process of run id again in the same tab.
func (r *Runs) Rerun(id string) {
	r.c.Go(func(ctx context.Context) func() {
		var info run.Info
		err := r.c.call(ctx, "run.rerun", api.RunIDParams{RunID: id}, &info)
		return func() {
			if err != nil {
				r.c.Status.Notice = "Rerun: " + err.Error()
				return
			}
			r.upsert(info)
			r.Started = true
		}
	})
}

// CloseTab closes the tab of run id; an active run is stopped first and
// its tab closes when it ended.
func (r *Runs) CloseTab(id string) {
	t := r.Tab(id)
	if t == nil {
		return
	}
	if t.Active() {
		t.closeOnExit = true
		r.Stop(id)
		return
	}
	r.drop(id)
	r.c.Go(func(ctx context.Context) func() {
		_ = r.c.call(ctx, "run.remove", api.RunIDParams{RunID: id}, nil)
		return nil
	})
}

// StopAll stops the active runs of project root (the one a close check
// was made for, see api.RunCloseCheck.Project); done (optional) runs on
// the UI goroutine once the backend stopped them.
func (r *Runs) StopAll(project string, done func()) {
	r.closeDecision("run.stopAll", project, done)
}

// KeepAll marks the active runs of project root to keep running when
// Tiegel closes ("Keep Running"); done (optional) runs afterwards.
func (r *Runs) KeepAll(project string, done func()) {
	r.closeDecision("run.keepAll", project, done)
}

func (r *Runs) closeDecision(method, project string, done func()) {
	r.c.Go(func(ctx context.Context) func() {
		err := r.c.call(ctx, method, api.RunStopAllParams{Project: project}, nil)
		return func() {
			r.c.notice(err)
			if done != nil {
				done()
			}
		}
	})
}

// CloseCheck asks the backend for active runs and the close policy;
// done runs on the UI goroutine.
func (r *Runs) CloseCheck(done func(api.RunCloseCheck)) {
	r.c.Go(func(ctx context.Context) func() {
		var res api.RunCloseCheck
		if err := r.c.call(ctx, "run.closeCheck", nil, &res); err != nil {
			res = api.RunCloseCheck{Policy: run.CloseKeep}
		}
		return func() { done(res) }
	})
}

// RunState is a short state text of a run ("running", "exit 0", …).
func RunState(i run.Info) string {
	switch i.State {
	case run.StateRunning:
		return "running"
	case run.StateStopped:
		return "stopped"
	}
	return fmt.Sprintf("exit %d", i.ExitCode)
}
