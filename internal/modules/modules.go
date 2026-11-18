// Package modules wires the compiled-in modules into an api.App:
//
//	php-runtime   interpreters, compose, PHP version, path mappings
//	php-features  feature/deprecation diagnostics (→ php-runtime)
//	lang-php      language server client + supervisor (php-runtime optional, via hooks)
//	lsp-phpactor, lsp-phpantom, lsp-intelephense
//	              language server plugins (contribute their server via hooks; D192)
//	git           git integration
//	debug-xdebug  Xdebug debugger (→ php-runtime)
//	syntax        highlighting, folds, outline over the API (D80)
//	php-index     Tree-sitter symbol index, navigation (nav.*, index.*; D111)
//	docker        Docker binary, contexts, engine status (docker.*; D230)
//	phpunit       test discovery, runs and results (test.*; D310)
//
// The modules never import each other; they share data through the bus and
// the hook points declared in internal/api/contract.go. None of them imports
// Gio: the GUI is a client of the API they register.
package modules

import (
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/debugxdebug"
	"github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/modules/git"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
	"github.com/ape-dev-de/tiegel/internal/modules/lspplugins"
	"github.com/ape-dev-de/tiegel/internal/modules/phpfeatures"
	"github.com/ape-dev-de/tiegel/internal/modules/phpindex"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
	"github.com/ape-dev-de/tiegel/internal/modules/phpunit"
	"github.com/ape-dev-de/tiegel/internal/modules/syntaxmod"
)

// Options configure all modules (tests replace single ones).
type Options struct {
	Runtime  phpruntime.Options
	Features phpfeatures.Options
	LangPHP  langphp.Options
	Git      git.Options
	Debug    debugxdebug.Options
	Index    phpindex.Options
	Docker   docker.Options
	PHPUnit  phpunit.Options
}

// All returns new instances of all compiled-in modules in dependency order.
func All(opts Options) []module.Module {
	mods := []module.Module{
		phpruntime.New(opts.Runtime),
		phpfeatures.New(opts.Features),
		langphp.New(opts.LangPHP),
	}
	for _, p := range lspplugins.All() {
		mods = append(mods, p)
	}
	return append(mods,
		git.New(opts.Git),
		debugxdebug.New(opts.Debug),
		syntaxmod.New(),
		phpindex.New(opts.Index),
		docker.New(opts.Docker),
		phpunit.New(opts.PHPUnit),
	)
}

// Installer returns an api.Options extension that adds all modules.
func Installer(opts Options) func(*api.App) error {
	return func(a *api.App) error {
		for _, m := range All(opts) {
			var ro []module.RegisterOption
			if d, ok := m.(interface{ RegisterOption() module.RegisterOption }); ok {
				ro = append(ro, d.RegisterOption()) // opt-in plugins (lsp-intelephense)
			}
			if err := a.AddModule(m, ro...); err != nil {
				return err
			}
		}
		return nil
	}
}

// Install adds all modules with default options to a.
func Install(a *api.App) error { return Installer(Options{})(a) }
