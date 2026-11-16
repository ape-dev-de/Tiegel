package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Find in Files and Replace in Path of CodeNav: search.inProject streams
// its matches as search.results events into FindState; replacing first
// asks for a preview (search.replaceInProject preview) and then applies
// it to the reviewed files.

// FindParams are the options of the Find in Files dialog.
type FindParams struct {
	Pattern       string
	Regex         bool
	CaseSensitive bool
	WholeWord     bool
	// Mask is a file mask ("*.php,!*Test.php").
	Mask string
	// Scope: "project" (default), "directory" (Dir) or "openFiles".
	Scope string
	Dir   string
	// Replace mode: Replacement is applied after a preview.
	Replace     bool
	Replacement string
}

func (p FindParams) search() api.ProjectSearchParams {
	sp := api.ProjectSearchParams{Mask: p.Mask, Scope: p.Scope, Dir: p.Dir}
	sp.Pattern, sp.Regex, sp.CaseSensitive, sp.WholeWord = p.Pattern, p.Regex, p.CaseSensitive, p.WholeWord
	if sp.Scope == "project" {
		sp.Scope = ""
	}
	return sp
}

// FindState is a running or finished project search.
type FindState struct {
	Params FindParams
	ID     string
	// Files are the matches received so far, in arrival order.
	Files   []api.FileMatches
	Matches int
	Running bool
	// Summary is set when the search ended (Cancelled, Limited, counts).
	Summary *api.ProjectSearchResult
	Err     string
	// Replace is the preview of Replace in Path (nil before it arrived).
	Replace *api.ProjectReplaceResult
	// Replaced is set after applying.
	Replaced *api.ProjectReplaceResult
}

// StartFind starts a streamed project search (a running one is
// cancelled). In replace mode the preview is requested when the search
// ends.
func (n *CodeNav) StartFind(p FindParams) {
	c := n.c
	if p.Pattern == "" {
		return
	}
	n.CancelFind()
	n.findSeq++
	id := fmt.Sprintf("gui-%p-%d", n, n.findSeq)
	st := &FindState{Params: p, ID: id, Running: true}
	n.Find = st
	n.FindVersion++
	sp := p.search()
	sp.Stream, sp.ID = true, id
	c.Go(func(ctx context.Context) func() {
		var r api.ProjectSearchResult
		err := c.call(ctx, "search.inProject", sp, &r)
		return func() {
			if err != nil && n.Find == st {
				st.Running, st.Err = false, err.Error()
				n.FindVersion++
			}
		}
	})
}

// handleSearchEvent applies a search.results batch.
func (n *CodeNav) handleSearchEvent(payload json.RawMessage) {
	st := n.Find
	if st == nil {
		return
	}
	var e api.ProjectSearchEvent
	if json.Unmarshal(payload, &e) != nil || e.ID != st.ID {
		return
	}
	for _, f := range e.Files {
		st.Files = append(st.Files, f)
		st.Matches += len(f.Matches)
	}
	if e.Error != "" {
		st.Err = e.Error
	}
	if e.Done {
		st.Running = false
		st.Summary = e.Summary
		if st.Params.Replace && e.Error == "" && (e.Summary == nil || !e.Summary.Cancelled) && st.Matches > 0 {
			n.previewReplace(st)
		}
	}
	n.FindVersion++
}

// CancelFind stops a running search.
func (n *CodeNav) CancelFind() {
	st := n.Find
	if st == nil || !st.Running {
		return
	}
	st.Running = false
	n.FindVersion++
	c := n.c
	id := st.ID
	c.Go(func(ctx context.Context) func() {
		_ = c.call(ctx, "search.cancel", api.SearchCancelParams{ID: id}, nil)
		return nil
	})
}

// previewReplace asks for the replacement preview of st.
func (n *CodeNav) previewReplace(st *FindState) {
	c := n.c
	rp := api.ProjectReplaceParams{ProjectSearchParams: st.Params.search(), Replacement: st.Params.Replacement, Preview: true}
	c.Go(func(ctx context.Context) func() {
		var r api.ProjectReplaceResult
		err := c.call(ctx, "search.replaceInProject", rp, &r)
		return func() {
			if n.Find != st {
				return
			}
			if err != nil {
				st.Err = err.Error()
			} else {
				st.Replace = &r
			}
			n.FindVersion++
		}
	})
}

// ApplyReplace replaces in the files of the preview (paths: a reviewed
// subset; nil means all previewed files).
func (n *CodeNav) ApplyReplace(paths []string) {
	st := n.Find
	if st == nil || st.Replace == nil {
		return
	}
	if paths == nil {
		for _, f := range st.Replace.Files {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	rp := api.ProjectReplaceParams{ProjectSearchParams: st.Params.search(), Replacement: st.Params.Replacement}
	rp.Paths = paths
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r api.ProjectReplaceResult
		err := c.call(ctx, "search.replaceInProject", rp, &r)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = fmt.Sprintf("Replaced %d occurrences in %d files", r.Replaced, r.FilesChanged)
			if r.Failed > 0 {
				c.Status.Notice += fmt.Sprintf(" (%d failed)", r.Failed)
			}
			if n.Find == st {
				st.Replaced = &r
				st.Replace = nil
				n.FindVersion++
			}
		}
	})
}
