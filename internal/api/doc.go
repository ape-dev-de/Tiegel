// Package api is Tiegel's transport-independent service layer (decision D41).
//
// An App owns the kernel (event bus, hooks, module registry, global and
// project settings, Local History, autosave pipeline) and exposes every
// backend function as a method in a namespace:
//
//	app.*      info, methods
//	events.*   subscribe, unsubscribe, publish
//	project.*  open, info, close
//	files.*    list, read, detectLanguage
//	doc.*      open, close, list, text, insert, delete, replace, undo, redo, save, saveAll
//	history.*  list, content, restore
//	search.*   inDoc, replaceInDoc
//	interp.*   list, select, version
//	compose.*  detect
//	lsp.*      servers, known
//	debug.*    listen, stop, sessions, continue, stack, eval, setBreakpoint
//	modules.*  list, enable, disable
//
// All methods take a context and a JSON-serialisable params struct and return
// a JSON-serialisable result. They live in one method table (name → handler)
// that every transport shares:
//
//   - App.Call / Session.Call for Go callers (CLI, GUI, tests),
//   - App.Serve for JSON-RPC 2.0 over a stream (`tiegel --headless serve`,
//     newline-delimited JSON or LSP Content-Length framing),
//   - the CLI in internal/headless (`tiegel --headless call <method> <json>`
//     plus friendly commands built on the same methods).
//
// Errors are *Error values with stable codes (CodeInvalidParams,
// CodeNotFound, CodeNoProject, ...). Transports map them to JSON-RPC error
// objects or exit codes.
//
// # Events
//
// Everything published on the bus can reach clients. A Session subscribes to
// topic patterns ("doc.*", "debug.*", "*") via Session.Subscribe or the
// "events.subscribe" method; Serve forwards them as JSON-RPC notifications
// with method "event" and params {"topic": ..., "payload": ...}. The API
// itself publishes the Topic* constants (doc.changed, doc.saved,
// debug.session.started, ...).
//
// # Adding a namespace
//
// New backend packages (gitcli, phpver, syntax, ...) only add a namespace;
// the transports pick it up automatically. Inside this package:
//
//  1. Create internal/api/<namespace>.go with params/result structs (JSON
//     tags, documented fields) and handler methods
//     func (a *App) gitStatus(ctx context.Context, p GitStatusParams) (GitStatus, error).
//  2. Add func (a *App) registerGit() that calls
//     mustHandle(a, "git.status", "One-line summary", a.gitStatus) per method,
//     and call it from New next to the other register* calls.
//  3. Return *Error for expected failures (Errorf(CodeNotFound, ...)),
//     publish state changes on the bus (a.bus.Publish("git.changed", ...)),
//     and use a.requireProject / a.resolvePath for project-relative paths.
//  4. Write tests against App.Call (see api_test.go), add a friendly CLI
//     command in internal/headless if it is useful in pipelines, and list
//     the methods in docs/headless.md.
//
// Code outside this package (tests, optional components) can register methods
// without touching New, through Options.Extensions:
//
//	api.New(api.Options{Extensions: []func(*api.App) error{
//		func(a *api.App) error {
//			return api.Handle(a, "demo.echo", "Echo params", func(ctx context.Context, p Echo) (Echo, error) { return p, nil })
//		},
//	}})
//
// The package never imports Gio; the GUI is just another client of App.
package api
