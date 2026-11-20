// Package git contains the Git UI organisms of Tiegel: the commit panel,
// the diff view, the commit log, the branch list and the three-way merge
// view. They are built only from internal/ui/{atoms,molecules,tokens}.
//
// # Data in, events out
//
// The organisms never talk to a repository. They do not know gitcli.Repo,
// run no git commands and do no I/O. The host (later the git module talking
// to internal/api, see D41) feeds them plain data and reacts to the intents
// they report:
//
//	Organism      data in (setters)                          events out
//	-----------   ----------------------------------------   ------------------------------------------
//	CommitPanel   SetChanges([]Change), SetDiff(path, diff), CommitRequested, AmendMessageRequested,
//	              SetBranch, SetGrouping, SetLastCommit,     DiffRequested, DiffOpenRequested,
//	              SetHistory(msgs), SetAmendMessage(msg),    RefreshRequested, RollbackRequested,
//	              SetBusy(bool), SetProblem(*Problem),       UpdateRequested, StashRequested,
//	              Reset(); FileGlyph, Stash (host hooks)     GroupingChanged, StashTabShown,
//	                                                         FileOpenRequested
//	DiffView      SetDiff(*gitcli.FileDiff),                 – (pure view; navigation is internal)
//	              WholeFileDiff(...) for complete files
//	LogView       SetCommits(c, more), AppendCommits(c, more) CommitSelected, LogPageRequested
//	BranchList    SetBranches([]gitcli.Branch)               CheckoutRequested, CreateBranchRequested,
//	                                                         DeleteBranchRequested
//	MergeView     SetMerge(path, *gitcli.MergeResult, lbl)   MergeApplied, MergeCancelled
//
// gitcli types (FileStatus, FileDiff, Hunk, HunkSelection, Commit, Branch,
// MergeResult, MergeBlock, PushError) are used as data types; the change
// tree is grouped with gitcli's pure BuildChangeTree, the same function
// the API (vcs.changes) and the diff tab's file order use. The commit
// panel shows no diff: DiffOpenRequested asks the host for an editor tab
// (organisms/diff.DiffWindow with file navigation).
//
// Every organism follows the component convention of internal/ui
// (Layout(gtx, th)). Update(gtx) returns the events produced since the
// last call. Rows of virtualized lists handle their input while being laid
// out, so their events are queued during Layout and returned by the next
// Update; the organism requests a new frame when it queued an event. A host
// therefore simply calls
//
//	for _, e := range panel.Update(gtx) { … }
//	panel.Layout(gtx, th)
//
// every frame.
//
// All lists (file list, diff lines, log, branches, merge rows) are
// virtualized with molecules.ScrollList: only visible rows are laid out, so
// diffs with tens of thousands of lines stay fast.
package git
