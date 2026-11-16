// Package core is the always-on kernel of Tiegel. It contains no UI code and
// never imports Gio; the packages below it are wired together by the
// application (cmd/tiegel) at startup.
//
// Building blocks:
//
//   - bus: in-process event bus. Modules publish and subscribe to topics
//     ("file.saved", "settings.changed") instead of calling each other.
//     Asynchronous (Publish) and synchronous (PublishSync) delivery, typed
//     topics via bus.Topic[T].
//   - hooks: extension points in the WordPress style. Actions notify,
//     filters transform values ("file.beforeSave"), guards may veto
//     ("commit.before"). Ordered by priority, each hook runs with a time
//     limit and panic protection so plugins cannot block the editor.
//   - module: the Module interface (ID, DependsOn, Start, Stop), the
//     Context modules receive (bus, hooks, settings, logger; extensible for
//     UI registrations) and the Registry that enables/disables modules with
//     dependency checks and starts/stops them in topological order.
//   - settings: internal settings store (decision D8), global under
//     os.UserConfigDir()/Tiegel and per project under
//     .../projects/<sha256 of project path>. Typed access, atomic writes,
//     change events on the bus, module on/off states.
//   - langdetect: language detection by file name, extension, shebang and
//     content (php, json, yaml, xml, dotenv, plaintext).
//   - history: Local History. Every save becomes a content-addressed,
//     deduplicated, gzip-compressed revision that can be listed, restored
//     and pruned. Stored per project below the settings project directory.
//   - trash: moves files to the desktop trash (macOS NSFileManager via
//     cgo, freedesktop.org trash on Linux) for files.delete.
//   - fspath: OS-aware path helpers (absolute/rooted paths, containment,
//     valid new file names incl. Windows rules).
//   - autosave: PhpStorm-style autosave (focus lost, before run/debug/commit,
//     optional idle timer) over an editor abstraction (DirtyDocs). Each save
//     runs filter "file.beforeSave", writes the file, records Local History
//     and fires action "file.saved".
//
// Typical wiring:
//
//	b := bus.New()
//	h := hooks.New()
//	base, _ := settings.DefaultBaseDir()
//	global, _ := settings.OpenGlobal(base, b)
//	reg := module.NewRegistry(module.Env{Bus: b, Hooks: h, Settings: global, Logger: log})
//	reg.Register(gitModule, module.DefaultEnabled())
//	reg.Start()
//
//	projDir, _ := settings.ProjectDir(base, projectPath)
//	hist, _ := history.Open(filepath.Join(projDir, "history"))
//	saver, _ := autosave.New(autosave.Config{Docs: editor, Hooks: h, History: hist})
//	autosave.SaveBeforeGuard(saver, h, commitBefore, autosave.TriggerBeforeCommit)
package core
