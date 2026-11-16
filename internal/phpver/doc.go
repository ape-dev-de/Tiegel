// Package phpver determines a project's PHP version and checks PHP sources
// against it.
//
// Version detection (Detect) follows the order of the concept: a user
// override, the real interpreter (via a Runner, locally or later inside a
// container), composer.json config.platform.php and finally the lower bound
// of composer.json require.php.
//
// The feature and deprecation table is embedded YAML (features.yaml): each
// entry maps a feature to its minimum PHP version (or a construct to the
// versions that deprecated/removed it) and a Tree-sitter query for the PHP
// grammar. A new PHP version means new table entries, not new code. A
// Checker compiles all queries into one and reports Diagnostics for a
// syntax.Document.
//
// The package does not import Gio.
package phpver
