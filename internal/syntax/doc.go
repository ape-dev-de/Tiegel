// Package syntax provides incremental parsing, syntax highlighting, folding
// regions and a structure outline on top of Tree-sitter.
//
// Supported languages are PHP (grammar "php", i.e. PHP embedded in HTML
// text), JSON, YAML and XML via the official Tree-sitter grammars, and dotenv
// files via a small built-in tokenizer. The upstream highlights.scm queries
// are embedded (see queries/NOTICE for their MIT licences) and their capture
// names are mapped onto a fixed list of Roles.
//
// # Threading
//
// Tree-sitter parsers, trees and query cursors are not thread-safe. This
// package encapsulates that: a Document owns its parser exclusively and
// guards parser and tree with mutexes, Reparse parses on a private copy of
// the tree (ts_tree_copy is cheap) and swaps the result in, and Snapshot hands
// out an independent tree copy for analyses on other goroutines. Compiled
// queries are immutable and shared. All C memory is released by
// Document.Close, Snapshot.Close and Query.Close.
//
// The package does not import Gio.
package syntax
