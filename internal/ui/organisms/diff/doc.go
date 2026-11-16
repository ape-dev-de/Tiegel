// Package diff contains the file comparison organisms of Tiegel: the
// DiffWindow (side-by-side or unified comparison of two versions of a
// file, built on internal/ui/editorview), the BlameGutter (annotation
// column for an editor) and the FileHistoryView (commits of a file with
// the diff of the selected one).
//
// # Data in, events out
//
// Like the git organisms they never talk to a repository. The host feeds
// them the results of the API methods git.fileDiff, git.blame and
// git.fileHistory (convert.go has JSON mirror types with converters) and
// reacts to their events:
//
//	Organism         data in                                   events out
//	---------------  ----------------------------------------  -------------------------------------------
//	DiffWindow       SetContent(Content, rightBuffer),         RevertRequested, IgnoreWhitespaceChanged,
//	                 SetBlocks([]Block)                        ViewModeChanged, RightEdited,
//	                                                           OpenFileRequested
//	BlameGutter      SetBlame(commits, lineHashes)             CommitClicked (HandleEditorEvent)
//	FileHistoryView  SetHistory, AppendHistory,                HistoryDiffRequested, HistoryPageRequested
//	                 Diff.SetContent                           (+ the DiffWindow's events)
//
// The right side of a DiffWindow is editable when the host passes its
// shared document buffer (target = work tree). While it is edited the
// window recomputes the change blocks locally with Compute, which runs the
// same algorithm as git.fileDiff (gitcli.CompareTexts).
//
// Every organism follows the component convention of internal/ui
// (Layout(gtx, th)); Update(gtx) returns the events since the last call.
package diff
