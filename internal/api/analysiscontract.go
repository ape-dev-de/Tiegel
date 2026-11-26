package api

import "time"

// Analysis results contract (D556): a plugin keeps the findings of a
// static analyzer for the project without running it – read from a
// report file or the analyzer's cache of the last command line run – and
// owns the namespace analysis.*: analysis.status, analysis.findings,
// analysis.import, analysis.reload, analysis.fix. The core and the GUI
// name no tool (D190): AnalysisStatus.Tool is the display name and
// AnalysisStatus.QATool the QA tool plugin whose runs the plugin folds
// into its findings, so clients show that tool's diagnostics only from
// here while the plugin runs.

// Topics of the analysis namespace.
const (
	// TopicAnalysisChanged: the findings or the source changed
	// (AnalysisStatus; fetch the findings with analysis.findings).
	TopicAnalysisChanged = "analysis.changed"
	// TopicWatchRefreshed: watch.refresh finished (e.g. the window got
	// the focus); modules check files outside the project then.
	TopicWatchRefreshed = "watch.refreshed"
)

// Analysis source kinds (AnalysisSource.Kind).
const (
	AnalysisSourceReport = "report"
	AnalysisSourceCache  = "cache"
	AnalysisSourceRun    = "run"
)

// AnalysisSource is a place analysis results can come from.
type AnalysisSource struct {
	// Kind is report or cache.
	Kind string `json:"kind"`
	// Path is the project relative or absolute file; File is absolute
	// ("" when it cannot be located on the host).
	Path string `json:"path"`
	File string `json:"file,omitempty"`
	// Format is json, checkstyle, junit or resultCache.
	Format string `json:"format,omitempty"`
	// Exists is true when the file is there; Modified is its mtime.
	Exists   bool      `json:"exists"`
	Modified time.Time `json:"modified,omitempty"`
	// Origin says why it is listed: "setting", "import", "found" (a
	// conventional report path), "config" (tmpDir/resultCachePath of the
	// analyzer configuration) or "default".
	Origin string `json:"origin"`
	// Used marks the source the findings come from.
	Used bool `json:"used,omitempty"`
	// Version is the analyzer version that wrote a cache.
	Version string `json:"version,omitempty"`
	// Reason explains why a source cannot be used.
	Reason string `json:"reason,omitempty"`
}

// AnalysisStatus is the result of analysis.status (and the analysis.changed
// event).
type AnalysisStatus struct {
	// Sources are the candidates (used one first); Used is the source of
	// the findings (nil: none).
	Sources []AnalysisSource `json:"sources"`
	Used    *AnalysisSource  `json:"used,omitempty"`
	// Config is the analyzer configuration file read for the cache
	// location and ignoreErrors ("" = none).
	Config string `json:"config,omitempty"`
	// Count is the number of findings, Files the number of files, Stale
	// the findings in files changed after the source was written.
	Count int `json:"count"`
	Files int `json:"files"`
	Stale int `json:"stale,omitempty"`
	// Loaded is when the findings were read.
	Loaded time.Time `json:"loaded,omitempty"`
	// Messages are remarks of the last load (general errors of a
	// report, ignoreErrors counts that no longer fit, warnings).
	Messages []string `json:"messages,omitempty"`
	// Error is set when the source could not be read.
	Error string `json:"error,omitempty"`
	// Inline mirrors the plugin's setting to mark findings in the editor.
	Inline bool `json:"inline"`
	// Tool is the analyzer's display name; QATool the QA tool plugin
	// whose diagnostics the findings include (clients drop that tool's
	// qa.diagnostics while the plugin runs); Hint says how to get a
	// source when there is none.
	Tool   string `json:"tool"`
	QATool string `json:"qaTool,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// FixWithAI is true when an AI agent can fix findings (the agent
	// plugin runs and its program is available); FixReason says why not.
	FixWithAI bool   `json:"fixWithAI"`
	FixReason string `json:"fixReason,omitempty"`
	// Version counts the loads (clients refetch findings when it moves).
	Version uint64 `json:"version"`
}

// AnalysisFinding is one finding of the analyzer in a file. Start and End are byte
// offsets in the current text (open document or file): the expression
// the message names or the line content.
type AnalysisFinding struct {
	Line       int    `json:"line"`
	Message    string `json:"message"`
	Identifier string `json:"identifier,omitempty"`
	Tip        string `json:"tip,omitempty"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	// Stale is set when the file changed after the source was written:
	// the finding may be outdated.
	Stale bool `json:"stale,omitempty"`
}

// AnalysisFile are the findings of one file.
type AnalysisFile struct {
	Path     string            `json:"path"`
	File     string            `json:"file"`
	Findings []AnalysisFinding `json:"findings"`
}

// AnalysisFindingsParams are the params of analysis.findings.
type AnalysisFindingsParams struct {
	// Path limits the result to a file or directory ("" = all).
	Path string `json:"path,omitempty"`
}

// AnalysisFindings is the result of analysis.findings.
type AnalysisFindings struct {
	Files   []AnalysisFile `json:"files"`
	Count   int            `json:"count"`
	Version uint64         `json:"version"`
}

// AnalysisImportParams are the params of analysis.import.
type AnalysisImportParams struct {
	// File is a report or result cache to read now (project relative or
	// absolute); it stays the source until analysis.reload {reset:true}
	// or the project closes. Empty re-reads the configured source.
	File string `json:"file,omitempty"`
}

// AnalysisReloadParams are the params of analysis.reload.
type AnalysisReloadParams struct {
	// IfChanged only reads the source when its file changed since the
	// last load (cheap: one stat).
	IfChanged bool `json:"ifChanged,omitempty"`
	// Reset forgets an imported file (back to the setting or the cache).
	Reset bool `json:"reset,omitempty"`
}

// AnalysisFixParams are the params of analysis.fix.
type AnalysisFixParams struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	// Identifier picks one of several findings of the line ("" = all of
	// the line).
	Identifier string `json:"identifier,omitempty"`
	// DryRun only builds the prompt (no agent needed).
	DryRun bool `json:"dryRun,omitempty"`
	// Wait and Ask are passed to agent.prompt (see AgentPromptParams).
	Wait bool   `json:"wait,omitempty"`
	Ask  string `json:"ask,omitempty"`
}

// AnalysisFixResult is the result of analysis.fix.
type AnalysisFixResult struct {
	// Prompt is the message sent to the agent; Findings the findings it
	// covers.
	Prompt   string            `json:"prompt"`
	Path     string            `json:"path"`
	Findings []AnalysisFinding `json:"findings"`
	// Sent is true when the prompt went to the agent; Turn is the
	// agent's answer to agent.prompt.
	Sent bool            `json:"sent"`
	Turn AgentTurnResult `json:"turn,omitempty"`
}
