package headless

import (
	"fmt"
	"io"

	gitmod "github.com/ape-dev-de/tiegel/internal/modules/git"
)

func init() {
	for name, takes := range map[string]bool{"m": true, "message": true, "amend": false, "push": false, "staged": false, "base": true, "remote": true} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "git-commit", usage: "git-commit -m MESSAGE [--amend] [--push] [FILE...]", summary: "commit FILEs (or the index) after saving documents; --push pushes afterwards", maxArgs: -1, cwdProject: true, run: runGitCommit},
		&command{name: "git-diff", usage: "git-diff [FILE...] [--staged] [--base REV]", summary: "unified diff of the work tree (or index/revision)", maxArgs: -1, cwdProject: true, run: runGitDiff},
		&command{name: "git-push", usage: "git-push [--remote R]", summary: "push the current branch (network access)", maxArgs: 0, cwdProject: true, run: runGitPush},
	)
}

func runGitCommit(c *cli) error {
	msg := c.p.value("message")
	if msg == "" {
		msg = c.p.value("m")
	}
	if msg == "" && !c.p.has("amend") {
		return usagef("usage: tiegel --headless %s", commands["git-commit"].usage)
	}
	var res gitmod.CommitSelectionResult
	if len(c.p.args) > 0 || c.p.has("amend") {
		params := gitmod.CommitSelectionParams{Message: msg, Paths: c.p.args, Amend: c.p.has("amend"), Push: c.p.has("push")}
		if err := c.call("git.commitSelection", params, &res); err != nil {
			return err
		}
	} else {
		var cr gitmod.GitCommitResult
		if err := c.call("git.commit", gitmod.GitCommitParams{Message: msg}, &cr); err != nil {
			return err
		}
		res.Hash, res.Message = cr.Hash, msg
		if c.p.has("push") {
			var pr gitmod.PushResult
			if err := c.call("git.push", gitmod.PushParams{Remote: c.p.value("remote")}, &pr); err != nil {
				res.PushError = &gitmod.PushErrorInfo{Kind: "failed", Message: err.Error()}
			} else {
				res.Pushed, res.Push = true, &pr
			}
		}
	}
	if err := c.emit(res, func(w io.Writer) {
		fmt.Fprintln(w, res.Hash)
		if res.Push != nil {
			fmt.Fprintf(w, "pushed %s to %s/%s\n", res.Push.Branch, res.Push.Remote, res.Push.RemoteBranch)
		}
	}); err != nil {
		return err
	}
	if res.PushError != nil {
		return fmt.Errorf("committed %s, but push failed (%s): %s", res.Hash, res.PushError.Kind, res.PushError.Message)
	}
	return nil
}

func runGitDiff(c *cli) error {
	params := gitmod.GitDiffParams{Paths: c.p.args, Staged: c.p.has("staged"), Base: c.p.value("base"), Raw: !c.json}
	var res gitmod.GitDiffResult
	if err := c.call("git.diff", params, &res); err != nil {
		return err
	}
	if c.json {
		return writeJSON(c.env.Stdout, res)
	}
	_, err := io.WriteString(c.env.Stdout, res.Raw)
	return err
}

func runGitPush(c *cli) error {
	var res gitmod.PushResult
	if err := c.call("git.push", gitmod.PushParams{Remote: c.p.value("remote")}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		fmt.Fprintf(w, "pushed %s to %s/%s\n", res.Branch, res.Remote, res.RemoteBranch)
	})
}
