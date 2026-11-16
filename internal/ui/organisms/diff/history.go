package diff

import (
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	gitui "github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// EmptyVersion is the git.fileDiff version without the file, used as the
// base of a root commit.
const EmptyVersion = "empty"

// FileHistoryView is the history of one file: the commits that changed it
// (the git LogView, paged) above a DiffWindow. Selecting a commit asks for
// its diff against the first parent; the host answers by filling Diff.
//
// Data in: SetHistory, AppendHistory, Diff.SetContent. Events out:
// HistoryDiffRequested, HistoryPageRequested and the events of Diff.
type FileHistoryView struct {
	// Diff shows the selected commit's change.
	Diff *DiffWindow

	path    string
	entries []gitcli.FileCommit
	log     *gitui.LogView
	split   molecules.Split
	q       queue
}

// NewFileHistoryView returns an empty history view.
func NewFileHistoryView() *FileHistoryView {
	return &FileHistoryView{Diff: NewDiffWindow(), log: gitui.NewLogView(), split: molecules.Split{Vertical: true, Ratio: 0.35}}
}

// SetHistory shows the first page of the history of path; more tells
// whether further pages exist.
func (v *FileHistoryView) SetHistory(path string, entries []gitcli.FileCommit, more bool) {
	v.path = path
	v.entries = append(v.entries[:0], entries...)
	v.log.SetCommits(commitsOf(entries), more)
}

// AppendHistory adds the next page (answer to HistoryPageRequested).
func (v *FileHistoryView) AppendHistory(entries []gitcli.FileCommit, more bool) {
	v.entries = append(v.entries, entries...)
	v.log.AppendCommits(commitsOf(entries), more)
}

func commitsOf(es []gitcli.FileCommit) []gitcli.Commit {
	out := make([]gitcli.Commit, len(es))
	for i := range es {
		out[i] = es[i].Commit
	}
	return out
}

// Path returns the file whose history is shown.
func (v *FileHistoryView) Path() string { return v.path }

// Len returns the number of commits shown.
func (v *FileHistoryView) Len() int { return len(v.entries) }

// SelectedHash returns the selected commit ("" if none).
func (v *FileHistoryView) SelectedHash() string { return v.log.SelectedHash() }

// Close releases the diff editors.
func (v *FileHistoryView) Close() { v.Diff.Close() }

// Update returns the events since the last call.
func (v *FileHistoryView) Update(gtx layout.Context) []Event {
	v.update(gtx)
	return append(v.q.drain(), v.Diff.Update(gtx)...)
}

func (v *FileHistoryView) update(gtx layout.Context) {
	for _, e := range v.log.Update(gtx) {
		switch e := e.(type) {
		case gitui.CommitSelected:
			if r, ok := v.request(e.Hash); ok {
				v.q.emit(r)
			}
		case gitui.LogPageRequested:
			v.q.emit(HistoryPageRequested{Path: v.path, Loaded: e.Loaded})
		}
	}
}

// request builds the diff request of a commit.
func (v *FileHistoryView) request(hash string) (HistoryDiffRequested, bool) {
	for i := range v.entries {
		e := &v.entries[i]
		if e.Hash != hash {
			continue
		}
		r := HistoryDiffRequested{Commit: e.Hash, Base: EmptyVersion, Path: e.Path, ParentPath: e.ParentPath()}
		if len(e.Parents) > 0 && e.Status != 'A' {
			r.Base = e.Parents[0]
		}
		if r.Path == "" {
			r.Path, r.ParentPath = v.path, v.path
		}
		return r, true
	}
	return HistoryDiffRequested{}, false
}

// Layout draws the view filling the maximum constraints.
func (v *FileHistoryView) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.update(gtx)
	gtx.Constraints.Min = gtx.Constraints.Max
	return v.split.Layout(gtx, th,
		func(gtx layout.Context) layout.Dimensions { return v.log.Layout(gtx, th) },
		func(gtx layout.Context) layout.Dimensions {
			if v.Diff.left == nil {
				return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("Select a commit to see its changes to this file.").Fn(th))
				})
			}
			return v.Diff.Layout(gtx, th)
		})
}
