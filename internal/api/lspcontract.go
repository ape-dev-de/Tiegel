package api

import (
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Language server plugins (decision D192) and installation recipes
// (decision D194). The lsp-* plugins contribute their server through
// HookLSPServers while they run; lang-php selects one of them and runs it
// under its supervisor. Plugins with an installable tool contribute a
// recipe through HookInstallRecipes; the kernel's install.* methods turn
// it into a previewable plan and run it only on explicit confirmation.

// Topics of the language server supervisor and the installer.
const (
	// TopicLSPState: the supervisor state changed (langphp.Status:
	// off, not-found, starting, running, crashed, stopped).
	TopicLSPState = "lsp.state"
	// TopicLSPLog: a line of the language server log (supervisor.Entry).
	TopicLSPLog = "lsp.log"
	// TopicInstallOutput: a line of a running installation (install.Line).
	TopicInstallOutput = "install.output"
	// TopicInstallDone: an installation finished (install.Result).
	TopicInstallDone = "install.done"
)

// LSPServerDecl is a language server contributed by a plugin.
type LSPServerDecl struct {
	servers.Definition
	// Plugin is the ID of the contributing module ("lsp-phpactor").
	Plugin string `json:"plugin"`
	// Order sorts the servers for automatic selection (lower first).
	Order int `json:"order"`
	// Path is the executable configured by the user (setting
	// "<plugin>.path"); it overrides the search.
	Path string `json:"path,omitempty"`
	// Args, if non-nil, replace Definition.Args (setting "<plugin>.args").
	Args []string `json:"args,omitempty"`
	// InitOptions are merged into the initializationOptions (setting
	// "<plugin>.initOptions").
	InitOptions map[string]any `json:"initOptions,omitempty"`
	// Note is shown when the server is chosen (e.g. license remarks).
	Note string `json:"note,omitempty"`
	// AutoSelect allows the automatic selection ("auto") to pick the
	// server; proprietary servers are only used when chosen explicitly.
	AutoSelect bool `json:"autoSelect"`
}

// EffectiveArgs returns the arguments the server is started with: the
// user's override (Args) if set, else the plugin's defaults
// (Definition.Args, e.g. "language-server" or "--stdio").
func (d LSPServerDecl) EffectiveArgs() []string {
	if d.Args != nil {
		return d.Args
	}
	return d.Definition.Args
}

// Hook points of language server plugins and installation recipes.
var (
	// HookLSPServers collects the language servers of the running lsp-*
	// plugins. A provider appends its declaration to a copy of the slice.
	HookLSPServers = hooks.NewFilter[[]LSPServerDecl]("lsp.servers")
	// HookInstallRecipes collects the installation recipes of the running
	// plugins. A provider appends its recipe to a copy of the slice.
	HookInstallRecipes = hooks.NewFilter[[]install.Recipe]("install.recipes")
)
