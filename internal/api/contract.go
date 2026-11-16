package api

import (
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// This file is the contract between the kernel and the modules: bus topics
// with their payload types and the hook points modules use to exchange data
// without calling each other.

// Topics published by the kernel and the modules (in addition to the
// Topic* constants in events.go).
const (
	// TopicFileOpened: a file was opened as document (FileOpenedEvent).
	TopicFileOpened = "file.opened"
	// TopicFileSaved: a document was written to disk, manually or by
	// autosave (FileSavedEvent).
	TopicFileSaved = "file.saved"
	// TopicDocEdited: one buffer change, from any client (DocEditEvent).
	TopicDocEdited = "doc.edited"
	// TopicInterpreterChanged: the project's interpreter changed
	// (InterpreterChangedEvent), published by php-runtime on interp.select.
	TopicInterpreterChanged = "interpreter.changed"
	// TopicPHPVersionChanged: the version override changed
	// (PHPVersionChangedEvent).
	TopicPHPVersionChanged = "php.versionChanged"
	// TopicMappingsChanged: host/container path mappings changed
	// (MappingsChangedEvent).
	TopicMappingsChanged = "runtime.mappingsChanged"
	// TopicPHPDiagnostics: feature/deprecation findings of a document
	// (PHPDiagnosticsEvent), published by php-features.
	TopicPHPDiagnostics = "php.diagnostics"
	// TopicLSPDiagnostics: diagnostics of the language server
	// (LSPDiagnosticsEvent), published by lang-php.
	TopicLSPDiagnostics = "lsp.diagnostics"
	// TopicLSPStatus: the language server started or stopped (LSPStatus).
	TopicLSPStatus = "lsp.status"
	// TopicDebugSessionStarted / Ended: a debug session began or ended.
	TopicDebugSessionStarted = "debug.sessionStarted"
	TopicDebugSessionEnded   = "debug.sessionEnded"
	// TopicDebugBreak: the debugger stopped (DebugBreakEvent, host paths).
	TopicDebugBreak = "debug.break"
	// TopicDebugBreakpointHit: the stop location is a project breakpoint
	// (BreakpointHitEvent).
	TopicDebugBreakpointHit = "debug.breakpointHit"
	// TopicDebugBreakpoints: the project breakpoints changed
	// (list of breakpoints).
	TopicDebugBreakpoints = "debug.breakpoints.changed"
	// TopicGitChanged: the work tree or index changed through the API
	// (GitChangedEvent).
	TopicGitChanged = "git.changed"
)

// ProjectEvent is the payload of project.opened and project.closed.
type ProjectEvent struct {
	Root string `json:"root"`
	Name string `json:"name,omitempty"`
}

// FileOpenedEvent is the payload of file.opened.
type FileOpenedEvent struct {
	// Path is the display path (project relative), File the absolute path.
	Path     string `json:"path"`
	File     string `json:"file"`
	Language string `json:"language"`
	Version  uint64 `json:"version"`
	Size     int    `json:"size"`
}

// FileSavedEvent is the payload of file.saved.
type FileSavedEvent struct {
	Path    string `json:"path"`
	File    string `json:"file"`
	Trigger string `json:"trigger"`
	Size    int    `json:"size"`
}

// TextPoint is a 0-based line and byte column.
type TextPoint struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// UTF16Pos is a 0-based line and UTF-16 code unit column (LSP coordinates).
type UTF16Pos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// UTF16Range is a range in LSP coordinates.
type UTF16Range struct {
	Start UTF16Pos `json:"start"`
	End   UTF16Pos `json:"end"`
}

// DocEditEvent is the payload of doc.edited: one change of a buffer, in the
// coordinates of the text before the change. The fields mirror
// buffer.Edit (Tree-sitter input edit) plus the LSP range of the replaced
// text, so consumers can update syntax trees and language servers
// incrementally. Version is the document version after the change; every
// change increases it by one.
type DocEditEvent struct {
	Path        string     `json:"path"`
	File        string     `json:"file"`
	Language    string     `json:"language"`
	Version     uint64     `json:"version"`
	Start       int        `json:"start"`
	OldEnd      int        `json:"oldEnd"`
	NewEnd      int        `json:"newEnd"`
	StartPoint  TextPoint  `json:"startPoint"`
	OldEndPoint TextPoint  `json:"oldEndPoint"`
	NewEndPoint TextPoint  `json:"newEndPoint"`
	Range       UTF16Range `json:"range"`
	Text        string     `json:"text"`
	OldText     string     `json:"oldText"`
}

// InterpreterChangedEvent is the payload of interpreter.changed.
type InterpreterChangedEvent struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Previous string `json:"previous,omitempty"`
}

// PHPVersionChangedEvent is the payload of php.versionChanged.
type PHPVersionChangedEvent struct {
	// Override is the new override ("" = removed).
	Override string `json:"override"`
}

// MappingInfo is a host/container path mapping.
type MappingInfo struct {
	Host   string `json:"host"`
	Remote string `json:"remote"`
}

// MappingsChangedEvent is the payload of runtime.mappingsChanged.
type MappingsChangedEvent struct {
	Mappings []MappingInfo `json:"mappings"`
	Source   string        `json:"source"`
}

// DebugBreakEvent is the payload of debug.break: where the debugger stopped,
// mapped to the host.
type DebugBreakEvent struct {
	Session string `json:"session"`
	// Path is the display path, File the absolute host path.
	Path      string `json:"path,omitempty"`
	File      string `json:"file,omitempty"`
	RemoteURI string `json:"remoteUri,omitempty"`
	Line      int    `json:"line,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Command   string `json:"command,omitempty"`
	Exception string `json:"exception,omitempty"`
	Message   string `json:"message,omitempty"`
}

// BreakpointHitEvent is the payload of debug.breakpointHit.
type BreakpointHitEvent struct {
	DebugBreakEvent
	// Breakpoint is the ID of the project breakpoint.
	Breakpoint string `json:"breakpoint"`
	Condition  string `json:"condition,omitempty"`
}

// GitChangedEvent is the payload of git.changed.
type GitChangedEvent struct {
	// Op is the operation, e.g. "stage", "commit", "checkout".
	Op    string   `json:"op"`
	Paths []string `json:"paths,omitempty"`
}

// CommitRequest is the payload of the commit.before guard.
type CommitRequest struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths,omitempty"`
	Amend   bool     `json:"amend,omitempty"`
	Push    bool     `json:"push,omitempty"`
}

// DebugRequest is the payload of the debug.before guard.
type DebugRequest struct {
	Addr string `json:"addr,omitempty"`
}

// PHPVersionQuery is the value of the php.resolveVersion filter. A provider
// (php-runtime) fills in the version; consumers (lang-php, php-features)
// apply the filter and fall back to their defaults if Version stays empty.
type PHPVersionQuery struct {
	Version string
	Short   string
	Source  string
	Detail  string
}

// PathMappingsQuery is the value of the runtime.pathMappings filter, filled
// by php-runtime with the mappings of the selected interpreter.
type PathMappingsQuery struct {
	Mappings []MappingInfo
	Source   string
}

// Hook points shared by kernel and modules.
var (
	// HookCommitBefore runs before a commit; returning an error aborts it.
	// The kernel saves all dirty documents here (autosave before commit).
	HookCommitBefore = hooks.NewGuard[CommitRequest]("commit.before")
	// HookCommitMessage may rewrite the commit message.
	HookCommitMessage = hooks.NewFilter[string]("commit.message")
	// HookDebugBefore runs before the debugger starts listening; the kernel
	// saves all dirty documents here.
	HookDebugBefore = hooks.NewGuard[DebugRequest]("debug.before")
	// HookPHPVersion resolves the project's PHP version.
	HookPHPVersion = hooks.NewFilter[PHPVersionQuery]("php.resolveVersion")
	// HookPathMappings resolves host/container path mappings.
	HookPathMappings = hooks.NewFilter[PathMappingsQuery]("runtime.pathMappings")
)
