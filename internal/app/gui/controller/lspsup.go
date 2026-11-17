package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Language server supervisor and tool installation (M11): the status bar
// entry, its popup menu and the "Language Server" tool window read
// Controller.LSP; the methods below call lsp.* and install.*.

// LSP states (lsp.state).
const (
	LSPOff      = "off"
	LSPNotFound = "not-found"
	LSPStarting = "starting"
	LSPRunning  = "running"
	LSPCrashed  = "crashed"
	LSPStopped  = "stopped"
)

// LSPServer is a language server of an enabled plugin (lsp.servers).
type LSPServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Plugin      string `json:"plugin"`
	Path        string `json:"path"`
	License     string `json:"license"`
	Proprietary bool   `json:"proprietary"`
	Found       bool   `json:"found"`
	Error       string `json:"error"`
	Selected    bool   `json:"selected"`
	Note        string `json:"note"`
}

// LSPLogEntry is a line of the server log (lsp.log).
type LSPLogEntry struct {
	Seq    int64  `json:"seq"`
	Kind   string `json:"kind"`
	Server string `json:"server"`
	Text   string `json:"text"`
}

// InstallPlan is the preview of an installation (install.plan).
type InstallPlan struct {
	Plugin              string   `json:"plugin"`
	Tool                string   `json:"tool"`
	Available           bool     `json:"available"`
	Scope               string   `json:"scope"`
	Scopes              []string `json:"scopes"`
	Display             string   `json:"display"`
	Dir                 string   `json:"dir"`
	ChangesComposerJSON bool     `json:"changesComposerJson"`
	Effects             []string `json:"effects"`
	Hint                string   `json:"hint"`
	Note                string   `json:"note"`
}

// InstallRun is a running or finished installation.
type InstallRun struct {
	RunID   string   `json:"runId"`
	Plugin  string   `json:"plugin"`
	Tool    string   `json:"tool"`
	State   string   `json:"state"`
	Display string   `json:"display"`
	Error   string   `json:"error"`
	Output  []string `json:"output"`
}

// Running reports whether the installation still runs.
func (r *InstallRun) Running() bool { return r != nil && r.State == "running" }

// LSPInfo is the language server state of the GUI.
type LSPInfo struct {
	// Status is the last supervisor state.
	Status LSPStatus
	// Servers are the servers of the enabled plugins (RefreshLSP).
	Servers []LSPServer
	// Log is the tail of the server log; LogVersion changes with it.
	Log        []LSPLogEntry
	LogVersion uint64
	// Install is the current or last installation.
	Install *InstallRun
	// Version changes whenever Status, Servers or Install change.
	Version uint64
}

// maxLogLines bounds the log kept by the GUI.
const maxLogLines = 2000

// applyLSPState follows a supervisor state.
func (c *Controller) applyLSPState(st LSPStatus) {
	c.LSP.Status = st
	c.LSP.Version++
	name := st.Name
	if name == "" {
		name = st.Server
	}
	c.Status.LSPRunning = st.State == LSPRunning || (st.State == "" && st.Running)
	switch st.State {
	case LSPRunning, "":
		if !c.Status.LSPRunning {
			return
		}
		c.Status.LSP = name
		c.Status.LSPHint = name + " running"
		if st.PHPVersion != "" {
			c.Status.LSPHint += " (PHP " + st.PHPVersion + ")"
		}
	case LSPStarting:
		c.Status.LSP, c.Status.LSPHint = name+": starting…", "Waiting for "+name+" to initialize"
	case LSPOff:
		c.Status.LSP, c.Status.LSPHint = "LSP off", orText(st.Reason, "No language server running")+" – click to choose a server"
	case LSPNotFound:
		c.Status.LSP = "No language server"
		if st.Server != "" && name != "" {
			c.Status.LSP = name + " not found"
		}
		c.Status.LSPHint = st.Reason + ". Tiegel never downloads one on its own – click for details, Install… or Choose Path…"
	case LSPCrashed:
		c.Status.LSP, c.Status.LSPHint = name+" crashed", st.Reason+" – restarting"
	case LSPStopped:
		c.Status.LSP, c.Status.LSPHint = name+" stopped", orText(st.Reason, "stopped")+" – click to restart"
	}
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// handleLSPEvent handles lsp.state, lsp.log and install.* events.
func (c *Controller) handleLSPEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "lsp.state":
		var st LSPStatus
		if json.Unmarshal(payload, &st) == nil {
			c.applyLSPState(st)
		}
	case "lsp.log":
		var e LSPLogEntry
		if json.Unmarshal(payload, &e) == nil {
			c.appendLog(e)
		}
	case "install.output":
		var l struct {
			RunID string `json:"runId"`
			Text  string `json:"text"`
		}
		if json.Unmarshal(payload, &l) == nil && c.LSP.Install != nil && c.LSP.Install.RunID == l.RunID {
			c.LSP.Install.Output = append(c.LSP.Install.Output, l.Text)
			c.LSP.Version++
		}
	case "install.done":
		var r InstallRun
		if json.Unmarshal(payload, &r) == nil && c.LSP.Install != nil && c.LSP.Install.RunID == r.RunID {
			c.LSP.Install = &r
			c.LSP.Version++
			c.Status.Notice = r.Tool + " installation " + r.State
			c.RefreshLSP()
		}
	}
}

func (c *Controller) appendLog(e LSPLogEntry) {
	if n := len(c.LSP.Log); n > 0 && c.LSP.Log[n-1].Seq >= e.Seq {
		return
	}
	c.LSP.Log = append(c.LSP.Log, e)
	if len(c.LSP.Log) > maxLogLines {
		c.LSP.Log = append([]LSPLogEntry(nil), c.LSP.Log[len(c.LSP.Log)-maxLogLines:]...)
	}
	c.LSP.LogVersion++
}

// RefreshLSP reloads the state and the servers of the enabled plugins.
func (c *Controller) RefreshLSP() {
	c.Go(func(ctx context.Context) func() {
		var st LSPStatus
		serr := c.call(ctx, "lsp.status", nil, &st)
		var list []LSPServer
		lerr := c.call(ctx, "lsp.servers", map[string]bool{"all": true}, &list)
		return func() {
			if serr == nil {
				c.applyLSPState(st)
			}
			if lerr == nil {
				c.LSP.Servers = list
				c.LSP.Version++
			}
		}
	})
}

// LoadLSPLog loads the log entries the GUI has not seen yet.
func (c *Controller) LoadLSPLog() {
	var after int64
	if n := len(c.LSP.Log); n > 0 {
		after = c.LSP.Log[n-1].Seq
	}
	c.Go(func(ctx context.Context) func() {
		var log []LSPLogEntry
		err := c.call(ctx, "lsp.log", map[string]any{"after": after, "limit": maxLogLines}, &log)
		return func() {
			if err != nil {
				return
			}
			for _, e := range log {
				c.appendLog(e)
			}
		}
	})
}

// lspCall runs an lsp.* method that returns the status.
func (c *Controller) lspCall(method string, params any) {
	c.Go(func(ctx context.Context) func() {
		var st LSPStatus
		err := c.call(ctx, method, params, &st)
		return func() {
			if st.State != "" {
				c.applyLSPState(st)
			}
			if err != nil && st.State == "" {
				c.notice(err)
			}
			c.RefreshLSP()
		}
	})
}

// SelectLSP chooses the server ("auto", "none" or an ID) for the project
// (or globally without a project).
func (c *Controller) SelectLSP(server string) {
	c.lspCall("lsp.select", map[string]string{"server": server})
}

// DisableLSP switches the language server off for the project.
func (c *Controller) DisableLSP() { c.lspCall("lsp.disable", nil) }

// SetLSPTrace switches the JSON-RPC trace into the log.
func (c *Controller) SetLSPTrace(on bool) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "lsp.trace", map[string]bool{"on": on}, nil)
		return func() {
			c.notice(err)
			if err == nil {
				c.LSP.Status.Trace = on
				c.LSP.Version++
			}
		}
	})
}

// SetLSPPath sets the executable of a server (setting <plugin>.path for
// the project) and re-selects.
func (c *Controller) SetLSPPath(server, path string) {
	c.lspCall("lsp.setPath", map[string]string{"server": server, "path": strings.TrimSpace(path)})
}

// PlanInstall previews the installation of a plugin's tool; done receives
// the plan (on the UI goroutine).
func (c *Controller) PlanInstall(plugin, scope string, done func(InstallPlan)) {
	c.Go(func(ctx context.Context) func() {
		var p InstallPlan
		err := c.call(ctx, "install.plan", map[string]string{"plugin": plugin, "scope": scope}, &p)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			done(p)
		}
	})
}

// RunInstall installs after the user confirmed the plan.
func (c *Controller) RunInstall(p InstallPlan) {
	c.Go(func(ctx context.Context) func() {
		var res struct {
			RunID string `json:"runId"`
		}
		err := c.call(ctx, "install.run", map[string]any{"plugin": p.Plugin, "scope": p.Scope, "confirm": true}, &res)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.LSP.Install = &InstallRun{RunID: res.RunID, Plugin: p.Plugin, Tool: p.Tool, State: "running", Display: p.Display}
			c.LSP.Version++
			c.Status.Notice = fmt.Sprintf("Installing %s: %s", p.Tool, p.Display)
		}
	})
}

// CancelInstall cancels the running installation.
func (c *Controller) CancelInstall() {
	r := c.LSP.Install
	if !r.Running() {
		return
	}
	id := r.RunID
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "install.cancel", map[string]string{"runId": id}, nil)
		return func() { c.notice(err) }
	})
}

// noticeNeedsLSPSuffix ends the notices of features that need a server;
// clicking such a notice opens the language server popup.
const noticeNeedsLSPSuffix = " needs a language server – click for details"

// NoticeNeedsLSP returns the notice of a feature that needs a server.
func NoticeNeedsLSP(feature string) string { return feature + noticeNeedsLSPSuffix }

// IsNeedsLSPNotice reports whether notice is a NoticeNeedsLSP text.
func IsNeedsLSPNotice(notice string) bool { return strings.HasSuffix(notice, noticeNeedsLSPSuffix) }
