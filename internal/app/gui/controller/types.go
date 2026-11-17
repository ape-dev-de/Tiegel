package controller

import "encoding/json"

// JSON shapes of the API results and events the GUI consumes. They mirror
// the documented wire format (docs/headless.md) instead of importing the
// module packages, so the GUI depends only on the protocol.

// ProjectInfo is the result of project.open/project.info.
type ProjectInfo struct {
	Open         bool     `json:"open"`
	Root         string   `json:"root"`
	Name         string   `json:"name"`
	Git          bool     `json:"git"`
	Composer     bool     `json:"composer"`
	ComposeFiles []string `json:"composeFiles"`
	Interpreter  string   `json:"interpreter"`
}

// FileNode is a node of files.list.
type FileNode struct {
	Name      string      `json:"name"`
	Path      string      `json:"path"`
	Dir       bool        `json:"dir"`
	Ignored   bool        `json:"ignored"`
	Library   bool        `json:"library"`
	Collapsed bool        `json:"collapsed"`
	Children  []*FileNode `json:"children"`
}

type filesListResult struct {
	Root      string    `json:"root"`
	Tree      *FileNode `json:"tree"`
	Truncated bool      `json:"truncated"`
}

// TextPos is an API position with all forms filled.
type TextPos struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Col    int `json:"col"`
}

// TextRange is an API range.
type TextRange struct {
	Start TextPos `json:"start"`
	End   TextPos `json:"end"`
}

// pos is the "at" parameter of the lsp.* methods (byte offset).
type pos struct {
	Offset int `json:"offset"`
}

type posParams struct {
	Path string `json:"path"`
	At   pos    `json:"at"`
}

type pathParams struct {
	Path string `json:"path"`
}

// DocState is the document state returned by doc.* methods.
type DocState struct {
	Path     string `json:"path"`
	Version  uint64 `json:"version"`
	Modified bool   `json:"modified"`
	Language string `json:"language"`
	EOL      string `json:"eol"`
}

// wireDiag is a diagnostic of lsp.diagnostics or php.diagnostics.
type wireDiag struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
	Code     string `json:"code"`
	Feature  string `json:"feature"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
}

// diagEvent is the payload of lsp.diagnostics and php.diagnostics.
type diagEvent struct {
	Path        string     `json:"path"`
	File        string     `json:"file"`
	Version     uint64     `json:"version"`
	PHPVersion  string     `json:"phpVersion"`
	Diagnostics []wireDiag `json:"diagnostics"`
}

// LSPStatus is the result of lsp.start/lsp.status and the lsp.status event.
type LSPStatus struct {
	Running    bool   `json:"running"`
	Server     string `json:"server"`
	Name       string `json:"name"`
	PHPVersion string `json:"phpVersion"`
	Error      string `json:"error"`
}

// PHPVersion is the result of php.version.
type PHPVersion struct {
	Version string `json:"version"`
	Short   string `json:"short"`
	Source  string `json:"source"`
	Detail  string `json:"detail"`
	// Hint explains a fallback, e.g. code HintContainerNotRunning when
	// the selected compose service is stopped and the version came from
	// composer.json (see DetectPHPStarting).
	Hint *Hint `json:"hint"`
}

// HintContainerNotRunning is the php.version hint code of a stopped
// compose service.
const HintContainerNotRunning = "container-not-running"

// Interpreter is an entry of interp.list.
type Interpreter struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Path     string `json:"path"`
	Service  string `json:"service"`
	Score    int    `json:"score"`
	Selected bool   `json:"selected"`
	Image    string `json:"image"`
	Remote   string `json:"remote"`
	Context  string `json:"context"`
	State    string `json:"state"`
}

// Mapping is a host/container path mapping.
type Mapping struct {
	Host   string `json:"host"`
	Remote string `json:"remote"`
}

// Mappings is the result of runtime.mappings.get/set.
type Mappings struct {
	Mappings []Mapping `json:"mappings"`
	Source   string    `json:"source"`
	Service  string    `json:"service"`
}

// Hint is a runtime.diagnose hint.
type Hint struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Diagnosis is the result of runtime.diagnose.
type Diagnosis struct {
	Version  string `json:"version"`
	ExecMode string `json:"execMode"`
	Hints    []Hint `json:"hints"`
}

// ModuleInfo is an entry of modules.list.
type ModuleInfo struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"dependsOn"`
	Enabled   bool     `json:"enabled"`
	Running   bool     `json:"running"`
	Error     string   `json:"error"`
}

// GitFile is an entry of git.status.
type GitFile struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	Conflict bool   `json:"conflict"`
}

type gitStatus struct {
	Branch   string    `json:"branch"`
	Head     string    `json:"head"`
	Detached bool      `json:"detached"`
	Files    []GitFile `json:"files"`
}

// CompletionItem is an item of lsp.completion.
type CompletionItem struct {
	Label         string     `json:"label"`
	Kind          string     `json:"kind"`
	Detail        string     `json:"detail"`
	Documentation string     `json:"documentation"`
	InsertText    string     `json:"insertText"`
	Snippet       bool       `json:"snippet"`
	SortText      string     `json:"sortText"`
	FilterText    string     `json:"filterText"`
	Deprecated    bool       `json:"deprecated"`
	Edit          *TextEdit  `json:"edit"`
	Additional    []TextEdit `json:"additionalEdits"`
}

// TextEdit replaces a range with text.
type TextEdit struct {
	Range   TextRange `json:"range"`
	NewText string    `json:"newText"`
}

type completionResult struct {
	Incomplete bool             `json:"incomplete"`
	Items      []CompletionItem `json:"items"`
}

type hoverResult struct {
	Found    bool   `json:"found"`
	Contents string `json:"contents"`
}

// Signature is one signature of lsp.signature.
type Signature struct {
	Label      string `json:"label"`
	Parameters []struct {
		Label string `json:"label"`
	} `json:"parameters"`
}

type signatureResult struct {
	Signatures      []Signature `json:"signatures"`
	ActiveSignature int         `json:"activeSignature"`
	ActiveParameter int         `json:"activeParameter"`
}

// Location is a result entry of lsp.definition/lsp.implementation.
type Location struct {
	Path  string    `json:"path"`
	File  string    `json:"file"`
	Range TextRange `json:"range"`
}

type settingValue struct {
	Found bool            `json:"found"`
	Value json.RawMessage `json:"value"`
}

type updateResult struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}
