// Package api is Tiegel's transport-independent service layer (decision D41).
//
// An App owns the kernel (event bus, hooks, module registry, global and
// project settings, Local History, autosave pipeline, open documents) and
// exposes every backend function as a method in a namespace. The kernel
// itself provides
//
//	app.*      info, version, methods
//	update.*   check (explicit request only; never automatic)
//	events.*   subscribe, unsubscribe, publish
//	project.*  open, info, close
//	files.*    list, read, detectLanguage, createFile, createDir, rename,
//	           move, copy, delete, reveal, copyPath, createPhpClass,
//	           phpNamespace (D131)
//	doc.*      open, close, list, text, insert, delete, replace, undo, redo, save, saveAll, resolveExternal
//	watch.*    start, stop, status, refresh (project file watcher, external changes)
//	history.*  list, content, restore
//	search.*   inDoc, replaceInDoc, inProject, cancel, replaceInProject
//	modules.*  list, enable, disable
//
// and the compiled-in modules (internal/modules) add theirs while they run:
//
//	php-runtime   interp.*, compose.*, php.version, runtime.*
//	php-features  php.check, php.diagnostics, php.setVersionOverride
//	lang-php      lsp.*
//	git           git.*
//	debug-xdebug  debug.*
//	syntax        syntax.highlights, syntax.folds, syntax.outline (D80)
//	php-index     index.*, nav.* (Tree-sitter symbol index, D111)
//
// All methods take a context and a JSON-serialisable params struct and return
// a JSON-serialisable result. They live in one method table (name → handler)
// that every transport shares:
//
//   - App.Call / Session.Call for Go callers (CLI, GUI, tests),
//   - App.Serve for JSON-RPC 2.0 over a stream (`tiegel --headless serve`,
//     newline-delimited JSON or LSP Content-Length framing),
//   - the CLI in internal/headless (`tiegel --headless call <method> <json>`
//     plus friendly commands built on the same methods),
//   - internal/api/server for sockets (`serve --listen unix:PATH|tcp:HOST:PORT`,
//     several clients, token authentication via the "auth" request).
//
// # Clients: Caller
//
// Client code (the GUI, docsync) is written against the Caller interface:
// Call(ctx, method, params, result) and Subscribe(ctx, topics, fn). InProcess
// implements it on an App in the same process (direct dispatch, no JSON when
// types match); internal/api/client implements it remotely (Dial for
// sockets, Spawn for a stdio child such as `wsl.exe -d D -- tiegel
// --headless serve`). The generic helper Call[R, P] gives typed results.
// Without a shared buffer, internal/api/docsync mirrors a document locally
// and keeps it in sync through versioned edits and doc.edited events.
//
// Errors are *Error values with stable codes (CodeInvalidParams,
// CodeNotFound, CodeNoProject, CodeModuleDisabled, ...) and optional Data.
// Transports map them to JSON-RPC error objects or exit codes.
//
// # Events
//
// Everything published on the bus can reach clients. A Session subscribes to
// topic patterns ("doc.*", "debug.*", "*") via Session.Subscribe or the
// "events.subscribe" method; Serve forwards them as JSON-RPC notifications
// with method "event" and params {"topic": ..., "payload": ...}. Kernel
// topics are the Topic* constants in events.go; the contract between kernel
// and modules (topics, payload types, hook points such as commit.before,
// debug.before, php.resolveVersion, runtime.pathMappings) is in contract.go.
//
// # Documents and threading
//
// An open document is a *Doc around one buffer.Buffer that all clients
// share: API handlers (doc.insert, ...), modules and the in-process GUI,
// which edits the buffer directly (App.Document). A buffer is not safe for
// concurrent use, so the rule is: every access to Doc.Buffer() happens
// between Doc.Lock and Doc.Unlock; the GUI takes the lock on its UI
// goroutine for each edit, API handlers take the same lock. Consumers never
// hook into the API handlers: the App registers a buffer.OnEdit listener on
// every document and publishes doc.edited (the change in Tree-sitter and LSP
// coordinates) and doc.changed (the new state) for every change, whoever
// made it. Listeners run while the lock is held; bus publishing only
// enqueues, so per-document event order equals edit order. Modules that need
// both the initial text and the changes subscribe to "doc.*" once (one
// subscription keeps the order), read the text with Doc.Snapshot on
// doc.opened and skip doc.edited events whose Version they already have.
//
// A document follows its file when files.rename or files.move renames it:
// the same *Doc (buffer, undo, generation, unsaved changes) gets the new
// path, announced as doc.renamed, doc.closed (old path, RenamedTo set) and
// doc.opened (new path). Consumers that cache a *Doc's path must re-read
// Doc.Path after doc.renamed. files.* operations validate that every path
// lies inside the project (also through symlinks) and publish
// files.changed.
//
// # Modules and namespaces
//
// A module (core/module.Module) receives a *ModuleContext in Start; AppFrom
// returns the App. It registers its methods with Provide (or a Provider) and
// its hooks with hooks.WithOwner(ctx.ModuleID()). When the module stops
// (modules.disable, dependency stopped, App.Close) its hooks and bus
// subscriptions (OnStop) are removed and its methods are disabled: calls
// fail with CodeModuleDisabled until it runs again. Methods listed by
// Claimer.Claims report CodeModuleDisabled even before the module ever ran.
// Modules never call each other; they exchange data over the bus and the
// hook points of contract.go.
//
//	func (m *Module) Start(ctx module.Context) error {
//		app, _ := api.AppFrom(ctx)
//		p := api.NewProvider(ctx)
//		api.Add(p, "demo.echo", "Echo params", m.echo)
//		ctx.OnStop(app.Bus().Subscribe("doc.*", m.onDoc))
//		return p.Err()
//	}
//
// Install modules with App.AddModule, typically through Options.Extensions
// (internal/modules.Installer does this for the compiled-in ones). Kernel
// namespaces are added inside this package with mustHandle in New; code
// outside can also register plain methods with Handle:
//
//	api.New(api.Options{Extensions: []func(*api.App) error{
//		func(a *api.App) error {
//			return api.Handle(a, "demo.echo", "Echo params", func(ctx context.Context, p Echo) (Echo, error) { return p, nil })
//		},
//	}})
//
// Every new function also gets a CLI command in internal/headless if it is
// useful in pipelines, and a line in docs/headless.md.
//
// The package never imports Gio; the GUI is just another client of App.
package api
