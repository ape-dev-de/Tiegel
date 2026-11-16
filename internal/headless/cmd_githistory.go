package headless

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
)

func init() {
	for name, takes := range map[string]bool{"target": true, "base-path": true, "ignore-whitespace": false, "skip": true, "max": true, "rev": true, "no-follow": false} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "git-file-diff", usage: "git-file-diff FILE [--base HEAD|index|working|REV] [--target working|index|REV] [--base-path PATH] [--ignore-whitespace]",
			summary: "change blocks of one file between two versions (default HEAD ↔ work tree)", minArgs: 1, maxArgs: 1, cwdProject: true, run: runGitFileDiff},
		&command{name: "git-history", usage: "git-history FILE [--skip N] [--max N] [--rev REV] [--no-follow]",
			summary: "commits that changed FILE, following renames", minArgs: 1, maxArgs: 1, cwdProject: true, run: runGitHistory},
		&command{name: "git-blame", usage: "git-blame FILE [--rev REV] [--ignore-whitespace]",
			summary: "annotate every line with the commit that last changed it", minArgs: 1, maxArgs: 1, cwdProject: true, run: runGitBlame},
		&command{name: "git-compare", usage: "git-compare BASE [TARGET]",
			summary: "files that differ between two revisions (without TARGET: the work tree)", minArgs: 1, maxArgs: 2, cwdProject: true, run: runGitCompare},
	)
}

func runGitFileDiff(c *cli) error {
	p := gitmod.FileDiffParams{Path: c.p.args[0], Base: c.p.value("base"), Target: c.p.value("target"),
		BasePath: c.p.value("base-path"), IgnoreWhitespace: c.p.has("ignore-whitespace")}
	var res gitmod.FileDiffResult
	if err := c.call("git.fileDiff", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) { writeFileDiff(w, &res) })
}

// writeFileDiff prints the blocks with their lines: "-" left, "+" right.
func writeFileDiff(w io.Writer, res *gitmod.FileDiffResult) {
	fmt.Fprintf(w, "--- %s\t%s\n+++ %s\t%s\n", res.Left.Path, res.Left.Label, res.Right.Path, res.Right.Label)
	switch {
	case res.Left.Binary || res.Right.Binary:
		fmt.Fprintln(w, "binary content differs")
		return
	case res.Identical:
		fmt.Fprintln(w, "identical")
		return
	}
	left, right := gitcli.SplitLines([]byte(res.Left.Text)), gitcli.SplitLines([]byte(res.Right.Text))
	print := func(prefix string, lines []string, from, to int) {
		for i := from - 1; i < to-1 && i < len(lines); i++ {
			fmt.Fprintf(w, "%s%s\n", prefix, strings.TrimRight(lines[i], "\r\n"))
		}
	}
	for _, b := range res.Blocks {
		fmt.Fprintf(w, "@@ %s left %s right %s @@\n", b.Kind, lineSpan(b.LeftStart, b.LeftEnd), lineSpan(b.RightStart, b.RightEnd))
		print("-", left, b.LeftStart, b.LeftEnd)
		print("+", right, b.RightStart, b.RightEnd)
	}
	fmt.Fprintf(w, "%d difference(s)\n", len(res.Blocks))
}

// lineSpan formats [start, end) (1-based) as "a-b", "a" or "before a".
func lineSpan(start, end int) string {
	switch {
	case end == start:
		return fmt.Sprintf("before %d", start)
	case end == start+1:
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d-%d", start, end-1)
}

func runGitHistory(c *cli) error {
	skip, err := c.p.intValue("skip")
	if err != nil {
		return err
	}
	maxN, err := c.p.intValue("max")
	if err != nil {
		return err
	}
	p := gitmod.FileHistoryParams{Path: c.p.args[0], Skip: skip, Max: maxN, Rev: c.p.value("rev"), NoFollow: c.p.has("no-follow")}
	var res gitmod.FileHistoryResult
	if err := c.call("git.fileHistory", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, e := range res.Commits {
			path := e.Path
			if e.OldPath != "" {
				path = e.OldPath + " → " + e.Path
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s %s\n", shortHash(e.Hash), e.Date.Local().Format("2006-01-02 15:04"), e.Author, e.Status, path, e.Subject)
		}
		_ = tw.Flush()
		if res.More {
			fmt.Fprintf(w, "more: --skip %d\n", skip+len(res.Commits))
		}
	})
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func runGitBlame(c *cli) error {
	p := gitmod.BlameParams{Path: c.p.args[0], Rev: c.p.value("rev"), IgnoreWhitespace: c.p.has("ignore-whitespace")}
	var res gitmod.BlameResult
	if err := c.call("git.blame", p, &res); err != nil {
		return err
	}
	if c.json {
		return writeJSON(c.env.Stdout, res)
	}
	var text string
	if p.Rev != "" {
		var sr gitmod.ShowResult
		if err := c.call("git.show", gitmod.ShowParams{Rev: p.Rev, Path: p.Path}, &sr); err != nil {
			return err
		}
		text = sr.Content
	} else {
		var fc api.FileContent
		if err := c.call("files.read", api.FilesReadParams{Path: p.Path}, &fc); err != nil {
			return err
		}
		text = fc.Content
	}
	lines := gitcli.SplitLines([]byte(text))
	commits := map[string]gitmod.BlameCommit{}
	width := 0
	for _, cm := range res.Commits {
		commits[cm.Hash] = cm
		width = max(width, len([]rune(cm.Author)))
	}
	width = min(width, 20)
	w := c.env.Stdout
	for _, l := range res.Lines {
		cm := commits[l.Hash]
		author, date := cm.Author, cm.Date.Local().Format(time.DateOnly)
		if cm.Uncommitted {
			author, date = "Not committed", "          "
		}
		if r := []rune(author); len(r) > width {
			author = string(r[:width])
		}
		txt := ""
		if l.Line-1 < len(lines) {
			txt = strings.TrimRight(lines[l.Line-1], "\r\n")
		}
		fmt.Fprintf(w, "%s (%-*s %s %4d) %s\n", shortHash(l.Hash), width, author, date, l.Line, txt)
	}
	return nil
}

func runGitCompare(c *cli) error {
	p := gitmod.CompareRevisionsParams{Base: c.p.args[0]}
	if len(c.p.args) > 1 {
		p.Target = c.p.args[1]
	}
	var res gitmod.CompareRevisionsResult
	if err := c.call("git.compareRevisions", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, f := range res.Files {
			if f.OldPath != "" {
				fmt.Fprintf(w, "%s\t%s → %s\n", f.Status, f.OldPath, f.Path)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\n", f.Status, f.Path)
		}
	})
}
