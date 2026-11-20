package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
)

// The commit window's change tree (vcs.changes) and diff tab
// (vcs.diffTab) as headless commands (AP18.4).

func init() {
	for name, takes := range map[string]bool{"group-by": true, "mode": true, "next": false, "prev": false} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "git-changes", usage: "git-changes [--group-by directory|module|none]",
			summary: "change tree of the commit window: changes and unversioned files with counts", maxArgs: 0, cwdProject: true, run: runGitChanges},
		&command{name: "git-diff-tab", usage: "git-diff-tab FILE [--mode local|staged|unstaged] [--next | --prev] [--group-by G] [--ignore-whitespace]",
			summary: "the diff editor tab of a change: compared versions, previous/next file and the change blocks", minArgs: 1, maxArgs: 1, cwdProject: true, run: runGitDiffTab},
	)
}

func runGitChanges(c *cli) error {
	var res api.VcsChangesResult
	if err := c.call("vcs.changes", api.VcsChangesParams{GroupBy: c.p.value("group-by")}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) { writeChangeTree(w, res.Tree) })
}

// writeChangeTree prints the groups indented; files as "state path".
func writeChangeTree(w io.Writer, t gitcli.ChangeTree) {
	var walk func(n *gitcli.ChangeNode, depth int)
	walk = func(n *gitcli.ChangeNode, depth int) {
		pad := strings.Repeat("  ", depth)
		switch n.Kind {
		case gitcli.NodeFile:
			p := n.Path
			if n.OrigPath != "" {
				p = n.OrigPath + " → " + p
			}
			fmt.Fprintf(w, "%s%-11s %s\n", pad, n.State, p)
		default:
			badge := ""
			if n.Badge != "" {
				badge = " [" + n.Badge + "]"
			}
			fmt.Fprintf(w, "%s%s  %s%s\n", pad, n.Name, files(n.Count), badge)
		}
		for _, ch := range n.Children {
			walk(ch, depth+1)
		}
	}
	walk(&t.Changes, 0)
	if t.Unversioned.Count > 0 {
		walk(&t.Unversioned, 0)
	}
}

func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func runGitDiffTab(c *cli) error {
	if c.p.has("next") && c.p.has("prev") {
		return usagef("--next and --prev exclude each other")
	}
	p := api.VcsDiffTabParams{Path: c.p.args[0], Mode: c.p.value("mode"), GroupBy: c.p.value("group-by"), IgnoreWhitespace: c.p.has("ignore-whitespace")}
	switch {
	case c.p.has("next"):
		p.Step = 1
	case c.p.has("prev"):
		p.Step = -1
	}
	var res api.VcsDiffTabResult
	if err := c.call("vcs.diffTab", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		fmt.Fprintf(w, "%s  [%d/%d]  prev: %s  next: %s\n", res.Title, res.Index+1, res.Total, res.Prev, res.Next)
		var fd gitmod.FileDiffResult
		if json.Unmarshal(res.Diff, &fd) == nil {
			writeFileDiff(w, &fd)
		}
	})
}
