// Package ideimport reads the settings of the reference IDE (decision
// D191, milestone M16) and turns them into a plan of Tiegel settings,
// keymap overrides and run configurations.
//
// The package only reads: it finds the application configuration
// directories (FindSources), parses their XML (ParseFile, Element),
// resolves path macros (Macros) and maps what it understands (ScanApp,
// ScanProject). Nothing is written here; the kernel (internal/api,
// "import.ide.*") applies a plan after the user asked for it, and only
// the areas the user selected. Nothing is read from the network.
//
// Components that belong to a plugin (a quality tool, the test runner,
// the debugger, Docker, Git …) are not interpreted here: a scan returns
// them as Component values and the kernel hands each one to the filter
// hook "import.ide.component", where the owning plugin turns it into
// items. The package therefore knows no tool by name.
//
// The package never imports Gio.
package ideimport
