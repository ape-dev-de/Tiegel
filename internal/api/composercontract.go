package api

// Composer contract (AP17.2, D410, D462). The kernel knows no Composer:
// the composer plugin registers the methods of the "composer" namespace
// (composer.scripts, composer.run). The types below are what clients
// (GUI, headless, serve) program against, so that no client imports the
// plugin package.

// ComposerScript is a script of the project's composer.json.
type ComposerScript struct {
	Name string `json:"name"`
	// Commands are the script's commands (a string value is one command).
	Commands []string `json:"commands"`
	// Description comes from "scripts-descriptions".
	Description string `json:"description,omitempty"`
	// Line is the 1-based line of the script's key in composer.json (for
	// the gutter run arrow; 0 if unknown).
	Line int `json:"line,omitempty"`
}

// ComposerScriptsResult is the result of composer.scripts.
type ComposerScriptsResult struct {
	// File is the composer.json of the project; Exists tells whether it
	// is there (no file: no scripts, no error).
	File    string           `json:"file"`
	Exists  bool             `json:"exists"`
	Scripts []ComposerScript `json:"scripts"`
	// Command is how Composer is started, e.g. "composer" or
	// "php composer.phar".
	Command string `json:"command"`
	// ConfigType is the run configuration type of Composer scripts and
	// ScriptField its field naming the script: clients build an unsaved
	// configuration of a script from them (Modify Run Configuration…).
	ConfigType  string `json:"configType"`
	ScriptField string `json:"scriptField"`
}

// ComposerRunParams are the params of composer.run.
type ComposerRunParams struct {
	Script string   `json:"script"`
	Args   []string `json:"args,omitempty"`
	// Interpreter overrides composer.interpreter (project, host, ID).
	Interpreter string `json:"interpreter,omitempty"`
}
