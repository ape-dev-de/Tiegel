// Package modules wires the compiled-in modules into an api.App:
//
//	php-runtime   interpreters, compose, PHP version, path mappings
//	php-features  feature/deprecation diagnostics (→ php-runtime)
//	lang-php      language server integration (php-runtime optional, via hooks)
//	git           git integration
//	debug-xdebug  Xdebug debugger (→ php-runtime)
//	syntax        highlighting, folds, outline over the API (D80)
//
// The modules never import each other; they share data through the bus and
// the hook points declared in internal/api/contract.go. None of them imports
// Gio: the GUI is a client of the API they register.
package modules

import (
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/debugxdebug"
	"github.com/ape-dev-de/tiegel/internal/modules/git"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
	"github.com/ape-dev-de/tiegel/internal/modules/phpfeatures"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
	"github.com/ape-dev-de/tiegel/internal/modules/syntaxmod"
)

// Options configure all modules (tests replace single ones).
type Options struct {
	Runtime  phpruntime.Options
	Features phpfeatures.Options
	LangPHP  langphp.Options
	Git      git.Options
	Debug    debugxdebug.Options
}

// All returns new instances of all compiled-in modules in dependency order.
func All(opts Options) []module.Module {
	return []module.Module{
		phpruntime.New(opts.Runtime),
		phpfeatures.New(opts.Features),
		langphp.New(opts.LangPHP),
		git.New(opts.Git),
		debugxdebug.New(opts.Debug),
		syntaxmod.New(),
	}
}

// Installer returns an api.Options extension that adds all modules.
func Installer(opts Options) func(*api.App) error {
	return func(a *api.App) error {
		for _, m := range All(opts) {
			if err := a.AddModule(m); err != nil {
				return err
			}
		}
		return nil
	}
}

// Install adds all modules with default options to a.
func Install(a *api.App) error { return Installer(Options{})(a) }
