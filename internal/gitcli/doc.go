// Package gitcli is Tiegel's Git backend. It drives the git command line
// tool (no libgit2, no Go reimplementation of the object store) and offers
// typed results for the IDE: repository detection, status, diffs, partial
// staging and committing, push, log, branches, gutter change markers and
// three-way merge data.
//
// All commands run through [Repo] with a fixed environment:
//
//   - LC_ALL=C so messages are parseable and stable,
//   - GIT_TERMINAL_PROMPT=0 and ssh BatchMode so git never blocks on a
//     credential prompt (credential helpers and ssh-agent still work),
//   - no pager, no colors, core.quotepath=off and literal pathspecs,
//   - NUL separated (-z) output wherever git offers it.
//
// The package is UI free (no Gio) and only uses the standard library. The
// pure algorithms ([DiffLines], [Merge3], [ParseDiff], [LineChanges]) do
// not need git at all.
//
// # Partial commits
//
// [Repo.CommitSelection] commits a selection of whole files and/or patches
// (built with [FileDiff.Patch] from hunks or single lines) without touching
// what the user staged for other files. Strategy:
//
//  1. A temporary index file is created inside the git directory and
//     filled with the tree of HEAD (git read-tree, GIT_INDEX_FILE).
//  2. Whole files are added to the temporary index with git add -A,
//     partial selections are applied with git apply --cached.
//  3. git commit runs against the temporary index, so the commit contains
//     exactly HEAD plus the selection.
//  4. Afterwards the entries of the committed paths in the user's real
//     index are reset to the new HEAD (git reset -q HEAD -- paths). Index
//     entries of all other paths stay exactly as the user left them; the
//     not committed rest of a partially committed file shows up as an
//     unstaged change.
//
// The temporary index is always removed, also on errors.
package gitcli
