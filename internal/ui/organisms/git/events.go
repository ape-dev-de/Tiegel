package git

import "github.com/ape-dev-de/tiegel/internal/gitcli"

// Event is an intent reported by a git organism. The concrete types below
// are the complete set; switch on them.
type Event interface{ gitEvent() }

// FileSelection is the part of one file chosen for a commit.
type FileSelection struct {
	// Path is the current path of the file.
	Path string
	// OrigPath is the source path of a rename or copy ("" otherwise).
	OrigPath string
	// Untracked marks a file from the "Unversioned" group.
	Untracked bool
	// Whole selects the complete change of the file (all hunks, renames,
	// mode changes, binary content). Hunks is nil then.
	Whole bool
	// Hunks selects parts of the diff the panel showed for Path. Lines are
	// indexes into Hunk.Lines (only added/deleted lines); nil Lines means
	// the whole hunk. The host builds the patch with FileDiff.Patch.
	Hunks []gitcli.HunkSelection
}

// CommitRequested asks to commit the selection (CommitPanel). With Push
// the host pushes after a successful commit. Files may be empty when
// Amend is set (reword the last commit only).
type CommitRequested struct {
	Message string
	Amend   bool
	Push    bool
	Files   []FileSelection
}

// AmendMessageRequested asks the host for the message of the last commit
// (CommitPanel, when Amend is switched on). Answer with
// CommitPanel.SetAmendMessage.
type AmendMessageRequested struct{}

// DiffRequested asks the host to load the diff of a file whose diff was not
// supplied with SetChanges (typically untracked files). Answer with
// CommitPanel.SetDiff.
type DiffRequested struct {
	Path      string
	Untracked bool
}

// RefreshRequested asks the host to reload status and diffs (CommitPanel
// toolbar).
type RefreshRequested struct{}

// FileOpenRequested asks to open a file in the editor (double click in the
// commit panel's file list).
type FileOpenRequested struct{ Path string }

// CommitSelected reports that the user selected a commit in the LogView.
type CommitSelected struct{ Hash string }

// LogPageRequested asks for the next page of the log (LogView scrolled to
// the end while more commits are available). Loaded is the number of
// commits shown so far, After the hash of the last one. Answer with
// LogView.AppendCommits.
type LogPageRequested struct {
	Loaded int
	After  string
}

// CheckoutRequested asks to check out a branch. For a remote branch the
// host typically creates a local tracking branch.
type CheckoutRequested struct {
	Branch string
	Remote bool
}

// CreateBranchRequested asks to create branch Name at Start (a branch
// name; "" means HEAD) and optionally check it out.
type CreateBranchRequested struct {
	Name     string
	Start    string
	Checkout bool
}

// DeleteBranchRequested asks to delete the local branch Name.
type DeleteBranchRequested struct{ Name string }

// MergeApplied delivers the merged content of a file (MergeView). If
// Unresolved > 0 the user deliberately applied it with conflict markers.
type MergeApplied struct {
	Path       string
	Content    []byte
	Unresolved int
}

// MergeCancelled reports that the user closed the merge without applying.
type MergeCancelled struct{ Path string }

func (CommitRequested) gitEvent()       {}
func (AmendMessageRequested) gitEvent() {}
func (DiffRequested) gitEvent()         {}
func (RefreshRequested) gitEvent()      {}
func (FileOpenRequested) gitEvent()     {}
func (CommitSelected) gitEvent()        {}
func (LogPageRequested) gitEvent()      {}
func (CheckoutRequested) gitEvent()     {}
func (CreateBranchRequested) gitEvent() {}
func (DeleteBranchRequested) gitEvent() {}
func (MergeApplied) gitEvent()          {}
func (MergeCancelled) gitEvent()        {}

// queue collects events between Update calls.
type queue struct{ events []Event }

func (q *queue) emit(e Event) { q.events = append(q.events, e) }

func (q *queue) drain() []Event {
	ev := q.events
	q.events = nil
	return ev
}
