package headless

import (
	"fmt"
	"io"

	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
)

func init() {
	for name, takes := range map[string]bool{"abort": false, "continue": false, "no-ff": false, "ff-only": false} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "git-merge", usage: "git-merge BRANCH [--no-ff|--ff-only] | git-merge --continue | git-merge --abort",
			summary: "merge BRANCH into the current branch; conflicts are listed (exit 1) for resolving and --continue", maxArgs: 1, cwdProject: true, run: runGitMerge},
		&command{name: "git-rebase", usage: "git-rebase BRANCH | git-rebase --continue | git-rebase --abort",
			summary: "rebase the current branch onto BRANCH; conflicts are listed (exit 1) for resolving and --continue", maxArgs: 1, cwdProject: true, run: runGitRebase},
	)
}

// branchArg returns the single BRANCH argument, "" without one.
func branchArg(c *cli) string {
	if len(c.p.args) == 1 {
		return c.p.args[0]
	}
	return ""
}

func runGitMerge(c *cli) error {
	p := gitmod.MergeParams{Branch: branchArg(c), NoFF: c.p.has("no-ff"), FFOnly: c.p.has("ff-only"),
		Continue: c.p.has("continue"), Abort: c.p.has("abort")}
	if n := count(p.Branch != "", p.Continue, p.Abort); n != 1 || p.NoFF && p.FFOnly {
		return usagef("usage: tiegel --headless %s", commands["git-merge"].usage)
	}
	var res gitmod.MergeOutcome
	if err := c.call("git.merge", p, &res); err != nil {
		return err
	}
	return emitOutcome(c, res)
}

func runGitRebase(c *cli) error {
	p := gitmod.RebaseParams{Onto: branchArg(c), Continue: c.p.has("continue"), Abort: c.p.has("abort")}
	if count(p.Onto != "", p.Continue, p.Abort) != 1 {
		return usagef("usage: tiegel --headless %s", commands["git-rebase"].usage)
	}
	var res gitmod.MergeOutcome
	if err := c.call("git.rebase", p, &res); err != nil {
		return err
	}
	return emitOutcome(c, res)
}

func count(set ...bool) int {
	n := 0
	for _, s := range set {
		if s {
			n++
		}
	}
	return n
}

// emitOutcome prints the result; stopping on conflicts fails the command
// so scripts notice it.
func emitOutcome(c *cli, res gitmod.MergeOutcome) error {
	if err := c.emit(res, func(w io.Writer) {
		switch {
		case res.Aborted:
			fmt.Fprintf(w, "%s aborted\n", res.Op)
		case len(res.Conflicts) > 0:
			fmt.Fprintf(w, "%s stopped with conflicts:\n", res.Op)
			for _, p := range res.Conflicts {
				fmt.Fprintf(w, "  %s\n", p)
			}
		case res.UpToDate:
			fmt.Fprintln(w, "already up to date")
		case res.FastForward:
			fmt.Fprintf(w, "fast-forward to %s\n", short(res.Head))
		default:
			fmt.Fprintf(w, "%s done, HEAD %s\n", res.Op, short(res.Head))
		}
	}); err != nil {
		return err
	}
	if len(res.Conflicts) > 0 {
		return fmt.Errorf("%s stopped with %d conflicted file(s); resolve them and run tiegel --headless git-%s --continue (or --abort)", res.Op, len(res.Conflicts), res.Op)
	}
	return nil
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
