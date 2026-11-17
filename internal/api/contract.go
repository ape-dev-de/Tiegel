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
	// TopicLSPStatus is the former name of TopicLSPState (lspcontract.go).
	TopicLSPStatus = TopicLSPState
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
	// TopicDockerContextChanged: the Docker context or binary used by
	// Tiegel changed (DockerContextChangedEvent), published by docker.
	TopicDockerContextChanged = "docker.contextChanged"
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

// FileChange is one entry of files.changed. Path/OldPath are display
// paths (project relative, slash separated), File/OldFile absolute; for
// "renamed" Path/File are the new location.
type FileChange struct {
	// Kind is created, modified, deleted or renamed.
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	File    string `json:"file"`
	OldPath string `json:"oldPath,omitempty"`
	OldFile string `json:"oldFile,omitempty"`
	Dir     bool   `json:"dir,omitempty"`
	// Trashed reports a delete that moved the entry to the trash.
	Trashed bool `json:"trashed,omitempty"`
	// Source is FileSourceTiegel for file operations through the API
	// (files.*) and FileSourceWatcher for changes the project watcher
	// found on disk (other programs, but also Tiegel's own saves).
	Source string `json:"source"`
}

// Change kinds and sources of FileChange.
const (
	FileCreated  = "created"
	FileModified = "modified"
	FileDeleted  = "deleted"
	FileRenamed  = "renamed"

	FileSourceTiegel  = "tiegel"
	FileSourceWatcher = "watcher"
)

// FilesChangedEvent is the payload of files.changed.
type FilesChangedEvent struct {
	Changes []FileChange `json:"changes"`
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

// DockerEndpointQuery is the value of the docker.endpoint filter: the
// docker module fills in the binary, the chosen context and the
// environment (PATH) for running docker; consumers (php-runtime) fall back
// to "docker" from PATH and docker's current context when it stays empty.
type DockerEndpointQuery struct {
	// Binary is the docker executable.
	Binary string
	// Context is the Docker context to pass as --context ("" = docker's
	// current context).
	Context string
	// Env are extra KEY=VALUE entries (PATH with the binary's directory and
	// the login shell PATH).
	Env []string
	// Provider is the detected engine (orbstack, docker-desktop, …), if
	// known without asking the engine.
	Provider string
}

// DockerContextChangedEvent is the payload of docker.contextChanged.
type DockerContextChangedEvent struct {
	// Context is the new effective context ("" = docker's current one).
	Context string `json:"context"`
	// Scope is "project" or "global".
	Scope    string `json:"scope"`
	Previous string `json:"previous,omitempty"`
}

// NavLSPQuery is the value of the nav.lsp filter: php-index asks the
// language server (lang-php) for definitions or implementations so that
// nav.definition and nav.implementations can merge both sources without
// the modules calling each other. A provider fills Locations and sets
// Answered; without a running server the value passes unchanged.
type NavLSPQuery struct {
	// Kind is "definition" or "implementation".
	Kind string
	// Path is the absolute path of the document, At the position.
	Path string
	At   Pos
	// Locations are the server's answer.
	Locations []NavLSPLocation
	Answered  bool
	// Err describes a failed server request ("" on success).
	Err string
}

// NavLSPLocation is one location of a NavLSPQuery answer.
type NavLSPLocation struct {
	Path  string    `json:"path"` // display path
	File  string    `json:"file"` // absolute path
	Range TextRange `json:"range"`
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
	// HookNavLSP asks the language server for definitions/implementations
	// (provided by lang-php, used by php-index).
	HookNavLSP = hooks.NewFilter[NavLSPQuery]("nav.lsp")
	// HookDockerEndpoint resolves the docker binary and context (provided
	// by docker, used by php-runtime for compose and image interpreters).
	HookDockerEndpoint = hooks.NewFilter[DockerEndpointQuery]("docker.endpoint")
)
